#!/usr/bin/env python3
"""Pin and stage relocatable native MSYS2 GTK/Python runtimes. No host install."""
import argparse, concurrent.futures, hashlib, io, json, os, pathlib, re, shutil, subprocess, tarfile, urllib.request

ENVIRONMENTS = {"amd64": ("ucrt64", "mingw-w64-ucrt-x86_64"), "arm64": ("clangarm64", "mingw-w64-clang-aarch64")}
SEEDS = ("gtk4", "python", "python-gobject", "python-cairo", "librsvg", "adwaita-icon-theme")

def digest(data): return hashlib.sha256(data).hexdigest()
def fetch(url, path, expected=None):
    path = pathlib.Path(path)
    if path.exists() and (expected is None or digest(path.read_bytes()) == expected): return path
    request = urllib.request.Request(url, headers={"User-Agent": "Quantway-Windows-Builder/1"})
    with urllib.request.urlopen(request, timeout=120) as response: content = response.read()
    if expected and digest(content) != expected: raise RuntimeError("SHA256 mismatch: " + url)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".download")
    temporary.write_bytes(content); temporary.replace(path)
    return path

def archive(path):
    try: return tarfile.open(path)
    except tarfile.ReadError:
        return tarfile.open(fileobj=io.BytesIO(subprocess.check_output(["zstd", "-d", "-c", str(path)])))

def fields(content):
    result = {}; name = None
    for line in content.splitlines():
        if line.startswith("%") and line.endswith("%"):
            name = line.strip("%"); result[name] = []
        elif line and name: result[name].append(line)
    return result

def lock_runtime(cache, output):
    result = {"schema": 1, "origin": "https://repo.msys2.org/mingw/", "authentication": "HTTPS database, SHA256 pinned packages; no claim of detached GPG verification", "architectures": {}}
    for arch, (environment, prefix) in ENVIRONMENTS.items():
        db = fetch(f"https://repo.msys2.org/mingw/{environment}/{environment}.db", cache / (environment + ".db"))
        packages = {}; providers = {}
        with archive(db) as tar:
            for member in tar.getmembers():
                if not member.name.endswith("/desc"): continue
                f = fields(tar.extractfile(member).read().decode())
                name = f["NAME"][0]; packages[name] = f
                for provide in f.get("PROVIDES", []): providers.setdefault(re.split(r"[<>=]", provide)[0], []).append(name)
        selected = {}; queue = [prefix + "-" + seed for seed in SEEDS]
        while queue:
            name = re.split(r"[<>=]", queue.pop())[0]
            if name not in packages:
                matches = sorted(providers.get(name, []))
                if len(matches) != 1: raise RuntimeError("ambiguous/missing dependency: " + name + " " + str(matches))
                name = matches[0]
            if name in selected: continue
            f = packages[name]; selected[name] = f
            queue.extend(f.get("DEPENDS", []))
        locked = []
        for name, f in sorted(selected.items()):
            locked.append({"name": name, "version": f["VERSION"][0], "file": f["FILENAME"][0], "sha256": f["SHA256SUM"][0], "size": int(f["CSIZE"][0]), "base": f["BASE"][0], "licenses": f.get("LICENSE", []), "homepage": f.get("URL", [""])[0], "depends": f.get("DEPENDS", [])})
        result["architectures"][arch] = {"environment": environment, "prefix": prefix, "database_sha256": digest(db.read_bytes()), "seeds": list(SEEDS), "packages": locked}
    total = sum(p["size"] for a in result["architectures"].values() for p in a["packages"])
    if total > 800_000_000: raise RuntimeError("runtime download exceeds 800 MB budget")
    output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"download_bytes": total, "packages": {a: len(v["packages"]) for a, v in result["architectures"].items()}}))

def runtime_file(relative):
    parts = relative.parts
    if not parts: return False
    if parts[0] == "bin": return relative.suffix.lower() in (".dll", ".exe")
    if parts[0] == "etc": return True
    if parts[0] == "lib":
        return len(parts) > 1 and (parts[1].startswith("python3.") or parts[1] in ("girepository-1.0", "gdk-pixbuf-2.0", "gio", "gtk-4.0", "gstreamer-1.0")) and relative.suffix.lower() not in (".a", ".la", ".pyc")
    return parts[0] == "share" and len(parts) > 1 and parts[1] in ("licenses", "glib-2.0", "icons", "mime", "themes", "locale", "fontconfig", "fonts", "xml", "gir-1.0")

def stage_runtime(lock, arch, cache, output):
    info = lock["architectures"][arch]; environment = info["environment"]
    def download(package):
        return fetch(f'https://repo.msys2.org/mingw/{environment}/{package["file"]}', cache / package["file"], package["sha256"])
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool: paths = list(pool.map(download, info["packages"]))
    output.mkdir(parents=True, exist_ok=True); extracted = {}
    metadata = output / "share" / "licenses" / "quantway-msys2"; metadata.mkdir(parents=True, exist_ok=True)
    for package, path in zip(info["packages"], paths):
        with archive(path) as tar:
            members = {m.name: m for m in tar.getmembers()}
            for member in members.values():
                p = pathlib.PurePosixPath(member.name)
                if p.is_absolute() or ".." in p.parts: raise RuntimeError("unsafe package member")
                if not p.parts or p.parts[0] != environment: continue
                relative = pathlib.PurePosixPath(*p.parts[1:])
                if not runtime_file(relative) or member.isdir(): continue
                actual = member
                for _ in range(8):
                    if not (actual.issym() or actual.islnk()): break
                    target = pathlib.PurePosixPath(actual.linkname)
                    target = target if actual.islnk() else pathlib.PurePosixPath(actual.name).parent / target
                    normalized = os.path.normpath(str(target)).replace("\\", "/")
                    if normalized.startswith("../") or normalized.startswith("/"): raise RuntimeError("unsafe symlink target")
                    actual = members[normalized]
                if not actual.isfile(): raise RuntimeError("unsupported runtime member: " + member.name)
                data = tar.extractfile(actual).read()
                key = str(relative); h = digest(data)
                if key in extracted and extracted[key] != h: raise RuntimeError("runtime file collision: " + key)
                destination = output / str(relative); destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(data); extracted[key] = h
        (metadata / (package["name"] + ".json")).write_text(json.dumps(package, indent=2) + "\n")
    # Tools normally create caches in pacman post-install hooks. Compile at first
    # launch with the bundled native tools; these files are staged by build/QA.
    for required in ("bin/pythonw.exe", "bin/python.exe", "bin/libgtk-4-1.dll", "lib/girepository-1.0/Gtk-4.0.typelib"):
        if not (output / required).is_file(): raise RuntimeError("runtime incomplete: " + required)
    (output / "MSYS2-RUNTIME.json").write_text(json.dumps({"architecture": arch, "environment": environment, "packages": info["packages"], "files": extracted}, indent=2) + "\n")
    print(json.dumps({"architecture": arch, "files": len(extracted), "bytes": sum((output / p).stat().st_size for p in extracted)}))

def main():
    parser = argparse.ArgumentParser(); parser.add_argument("action", choices=("lock", "stage")); parser.add_argument("--cache", type=pathlib.Path, required=True); parser.add_argument("--lock", type=pathlib.Path, required=True); parser.add_argument("--arch", choices=ENVIRONMENTS); parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    if args.action == "lock": lock_runtime(args.cache, args.lock)
    else:
        if not args.arch or not args.output: parser.error("stage requires --arch and --output")
        stage_runtime(json.loads(args.lock.read_text()), args.arch, args.cache, args.output)
if __name__ == "__main__": main()
