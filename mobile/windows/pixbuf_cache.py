"""Build a private image-loader cache from locked bundled module metadata."""
import hashlib
import json
import ntpath
import os
from pathlib import Path
import re
import subprocess
import tempfile

LOADERS = Path('runtime/lib/gdk-pixbuf-2.0/2.10.0/loaders')
MAX_CACHE = 256 * 1024
DLL_NAME = re.compile(r'[A-Za-z0-9_][A-Za-z0-9_.-]*\.dll\Z', re.ASCII)

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def query_records(text):
    """Only use module basenames; never reuse paths emitted by another install."""
    result = []
    current = None
    for line in text.splitlines():
        if line.startswith('"') and line.endswith('.dll"'):
            name = ntpath.basename(json.loads(line))
            if not DLL_NAME.fullmatch(name):
                raise ValueError('invalid loader basename')
            current = {'file': name, 'record': []}
            result.append(current)
        elif current is not None and line:
            current['record'].append(line)
    if not result:
        raise ValueError('no loader records')
    return [{'file': r['file'], 'record': '\n'.join(r['record'])} for r in result]

def render_records(root, modules):
    lines = ['# Quantway private bundled GdkPixbuf loader cache', '# No global cache or module directories are consulted.', '']
    names = set()
    for module in modules:
        name = module['file']
        record = module['record']
        if not DLL_NAME.fullmatch(name) or name.casefold() in names:
            raise ValueError('invalid or duplicate bundled loader')
        names.add(name.casefold())
        path = root / LOADERS / name
        if not path.is_file() or path.is_symlink():
            raise ValueError('bundled loader missing')
        if not isinstance(record, str) or not record.startswith('"') or '\n\n' in record or '\x00' in record or len(record) > 16384:
            raise ValueError('invalid loader metadata')
        lines.extend([json.dumps(path.as_posix(), ensure_ascii=False), record, ''])
    data = ('\n'.join(lines) + '\n').encode('utf-8')
    if len(data) > MAX_CACHE:
        raise ValueError('loader cache too large')
    return data

def from_manifest(root, manifest, arch, verify_hashes=False):
    if manifest.get('schema') != 1:
        raise ValueError('unsupported loader manifest')
    lock = root / 'licenses/msys2-runtime-lock.json'
    if sha(lock) != manifest['runtime_lock_sha256']:
        raise ValueError('loader manifest does not match bundled runtime lock')
    expected = manifest['architectures'][arch]['module_sha256']
    if set(expected) != {r['file'] for r in manifest['modules']}:
        raise ValueError('loader manifest module set mismatch')
    if verify_hashes:
        for name, checksum in expected.items():
            if not DLL_NAME.fullmatch(name) or sha(root / LOADERS / name) != checksum:
                raise ValueError('bundled loader hash mismatch')
    return render_records(root, manifest['modules'])

def write_cache(cache_dir, data, identity):
    cache_dir.mkdir(parents=True, exist_ok=True)
    path = cache_dir / ('pixbuf-' + identity + '.cache')
    if path.is_file() and path.stat().st_size <= MAX_CACHE and path.read_bytes() == data:
        return path
    fd, temporary = tempfile.mkstemp(prefix='.pixbuf-', suffix='.tmp', dir=cache_dir)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return path

def prepare_cache(root, cache_dir, warn=lambda message: None, runner=subprocess.run, timeout=3):
    """Packaged startup never launches a DLL-enumeration subprocess.

    The bounded nonfatal query path is only for incomplete developer bundles.
    Even that path pins every cache entry to the installation's own loader dir.
    """
    root = Path(root).resolve()
    source = 'bundled'
    try:
        manifest_bytes = (root / 'pixbuf-loaders.json').read_bytes()
        manifest = json.loads(manifest_bytes)
        arch = json.loads((root / 'BUILD-INFO.json').read_text())['architecture'].split('/')[1]
        data = from_manifest(root, manifest, arch)
        identity = hashlib.sha256(str(root).encode() + arch.encode() + manifest_bytes).hexdigest()[:16]
    except (OSError, ValueError, KeyError, IndexError, TypeError) as error:
        source = 'query'
        warn('Bundled image-loader metadata unavailable: ' + type(error).__name__)
        try:
            env = dict(os.environ, GDK_PIXBUF_MODULEDIR=str(root / LOADERS))
            env.pop('GDK_PIXBUF_MODULE_FILE', None)
            result = runner([str(root / 'runtime/bin/gdk-pixbuf-query-loaders.exe')], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
            if result.returncode:
                raise ValueError('loader query failed')
            data = render_records(root, query_records(result.stdout.decode('utf-8')))
        except (OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired):
            source = 'empty-fallback'
            warn('Image-loader query unavailable; GUI startup continues without a global loader cache.')
            data = b'# Quantway: optional image loaders unavailable on this launch\n'
        identity = hashlib.sha256(str(root).encode() + data).hexdigest()[:16]
    return write_cache(Path(cache_dir), data, identity), source
