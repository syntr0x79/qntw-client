#!/usr/bin/env python3
"""Bounded ordinary-user acceptance through the packaged macOS privilege path.

The private credential JSON contains a `link`; it is sent only over stdin.
macOS asks for administrator authorization. Every exit asks the helper to stop.
"""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import queue
import socket
import subprocess
import threading
import time


def command(argv, timeout=6, data=None):
    try:
        p = subprocess.run(argv, input=data, text=True, capture_output=True, timeout=timeout)
        return {"exit": p.returncode, "out": p.stdout.strip()[:1800], "error": p.stderr.strip()[:400]}
    except subprocess.TimeoutExpired:
        return {"exit": -1, "out": "timeout"}


def route():
    for line in command(["/sbin/route", "-n", "get", "1.1.1.1"])["out"].splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0] == "interface:":
            return fields[1]


def dns_keys():
    return command(["/usr/sbin/scutil"], data="list State:/Network/Service/Quantway-.*/DNS\nquit\n")["out"]


def probes(pinned):
    jobs = {
        "https": ["/usr/bin/curl", "--noproxy", "*", "-4", "--max-time", "4", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com/"],
        "udp_dns": ["/usr/bin/dig", "+time=2", "+tries=1", "@1.1.1.1", "example.com", "A"],
        "system_dns": ["/usr/bin/dscacheutil", "-q", "host", "-a", "name", "example.com"],
        "pinned_https": ["/usr/bin/curl", "--noproxy", "*", "-4", "--max-time", "4", "--resolve", "qntw.link:443:" + pinned, "-sS", "-o", "/dev/null", "-w", "%{http_code}", "https://qntw.link/healthz"],
    }
    before = route()
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        results = dict(zip(jobs, pool.map(command, jobs.values())))
    after = route()
    checks = {
        "https": results["https"]["exit"] == 0 and results["https"]["out"] == "200",
        "pinned_https": results["pinned_https"]["exit"] == 0 and results["pinned_https"]["out"] == "200",
        "udp_dns": results["udp_dns"]["exit"] == 0 and "status: NOERROR" in results["udp_dns"]["out"] and "ANSWER: 0" not in results["udp_dns"]["out"],
        "system_dns": results["system_dns"]["exit"] == 0 and "ip_address:" in results["system_dns"]["out"],
    }
    return {"route_before": before, "route_after": after,
            "same_system_tun": bool(before and before.startswith("utun") and before == after), "checks": checks,
            "probe_results": results}


def main():
    a = argparse.ArgumentParser(description=__doc__)
    a.add_argument("--app", required=True, type=Path)
    a.add_argument("--credentials", required=True, type=Path)
    a.add_argument("--output", required=True, type=Path)
    a.add_argument("--country", default="PL")
    a.add_argument("--seconds", type=int, default=45, choices=range(15, 56))
    args = a.parse_args()
    if os.geteuid() == 0:
        a.error("run as the ordinary desktop user")
    if args.credentials.stat().st_mode & 0o077:
        a.error("credentials must be private")
    link = json.loads(args.credentials.read_text())["link"]
    if not link.startswith("https://qntw.link/sub/"):
        a.error("expected Quantway device link")
    pinned = socket.getaddrinfo("qntw.link", 443, socket.AF_INET, socket.SOCK_STREAM)[0][4][0]
    baseline = {"route": route(), "dns_keys": dns_keys()}
    report = {"uid": os.geteuid(), "baseline": baseline, "events": [], "samples": [],
              "connected": False, "duration_complete": False}
    events = queue.Queue()
    helper = args.app / "Contents/Helpers/quantway-helper"
    p = subprocess.Popen([str(helper), "--json", "--action", "tinc-connect"], stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, bufsize=1)
    start = time.monotonic()
    connected_at = None
    stop = threading.Event()

    def watchdog():
        # Independent of the probes/output loop: bound authorization and session.
        if not stop.wait(110):
            try:
                p.stdin.write("stop\n")
                p.stdin.flush()
                p.stdin.close()
            except (OSError, ValueError):
                pass

    def read_events():
        for line in p.stdout:
            try:
                e = json.loads(line)
                events.put({k: e[k] for k in ("status", "stage", "code", "country") if k in e})
            except ValueError:
                pass

    threading.Thread(target=watchdog, daemon=True).start()
    reader_thread = threading.Thread(target=read_events, daemon=True)
    reader_thread.start()
    next_probe = 0
    try:
        p.stdin.write(json.dumps({"mode": "tinc", "link": link, "country": args.country}) + "\n")
        p.stdin.flush()
        while p.poll() is None and time.monotonic() - start < 100:
            now = time.monotonic()
            while not events.empty():
                event = events.get_nowait()
                event["elapsed_seconds"] = round(now - start, 2)
                report["events"].append(event)
                if "status" in event:
                    print(json.dumps({"event": event}), flush=True)
                if event.get("status") == "connected" and connected_at is None:
                    connected_at = now
                    report["connected"] = True
                if event.get("status") in ("failed", "expired", "stopped"):
                    raise RuntimeError("helper reported failure or expired access")
            if connected_at is not None:
                if now - connected_at >= args.seconds:
                    report["duration_complete"] = True
                    report["connected_seconds"] = round(now - connected_at, 2)
                    break
                if now >= next_probe:
                    sample = probes(pinned)
                    sample["elapsed_seconds"] = round(time.monotonic() - start, 2)
                    report["samples"].append(sample)
                    print(json.dumps(sample), flush=True)
                    next_probe = time.monotonic() + 6
            time.sleep(0.2)
    except Exception as e:
        report["error_type"] = type(e).__name__
    finally:
        try:
            p.stdin.write("stop\n")
            p.stdin.flush()
            p.stdin.close()
        except (OSError, ValueError):
            pass
        try:
            p.wait(timeout=20)
        except subprocess.TimeoutExpired:
            # Do not kill the elevated owner before it can restore the network.
            report["cleanup_timeout"] = True
        reader_thread.join(timeout=2)
        # The CLI can return zero while forwarding a final cleanup error. Read
        # the already-sanitized terminal events before declaring restoration.
        while not events.empty():
            event = events.get_nowait()
            event["elapsed_seconds"] = round(time.monotonic() - start, 2)
            report["events"].append(event)
            if "status" in event:
                print(json.dumps({"event": event}), flush=True)
        report["failure_reported"] = any(
            e.get("status") in ("failed", "expired") or e.get("code") == "network_cleanup_failed"
            for e in report["events"])
        report["cleanup_failure_reported"] = any(
            e.get("code") == "network_cleanup_failed" for e in report["events"])
        stop.set()
        time.sleep(1)
        report["after"] = {"route": route(), "dns_keys": dns_keys()}
        report["cleanup_verified"] = (report["after"] == baseline and p.poll() == 0
                                      and not report["cleanup_failure_reported"]
                                      and not report.get("cleanup_timeout", False)
                                      and not reader_thread.is_alive())
        report["passed"] = (report["connected"] and report["duration_complete"] and len(report["samples"]) >= 3
                            and all(s["same_system_tun"] and all(s["checks"].values()) for s in report["samples"])
                            and report["cleanup_verified"] and not report["failure_reported"]
                            and "error_type" not in report)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"passed": report["passed"], "samples": len(report["samples"]), "cleanup_verified": report["cleanup_verified"]}))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
