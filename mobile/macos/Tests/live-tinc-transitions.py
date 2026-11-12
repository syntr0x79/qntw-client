#!/usr/bin/env python3
"""One-helper macOS TUN acceptance: countries, GC, automatic exit replacement.

The failover callbacks are fixed local operator scripts. The trigger only checks
an already-armed plan; restoration runs after STOP and verified local cleanup.
Credentials are read privately and sent exclusively on the helper's stdin.
"""
import argparse
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import queue
import re
import socket
import subprocess
import sys
import threading
import time

spec = importlib.util.spec_from_file_location("live_tinc", Path(__file__).with_name("live-tinc.py"))
live = importlib.util.module_from_spec(spec)
spec.loader.exec_module(live)


def callback(path, egress, timeout=10):
    argv = ([sys.executable, str(path)] if path.suffix == ".py" else [str(path)]) + [egress]
    p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    if p.returncode:
        raise RuntimeError("operator_callback_failed")
    return json.loads(p.stdout)


class Session:
    def __init__(self, helper, link):
        self.started = time.monotonic()
        self.p = subprocess.Popen([str(helper), "--json", "--action", "tinc-connect"],
                                  stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.DEVNULL, text=True, bufsize=1)
        self.events = queue.Queue()
        self.history = []
        self.latest = None
        self.helper_pid = None
        self.ended = False
        self.input_lock = threading.Lock()
        self.finished = threading.Event()
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()
        self.guard = threading.Thread(target=self.watchdog, daemon=True)
        self.guard.start()
        self.send({"mode": "tinc", "link": link, "country": "PL", "acceptance_diagnostics": True})

    def read(self):
        for line in self.p.stdout:
            try:
                value = json.loads(line)
            except ValueError:
                continue
            event = {k: value[k] for k in ("status", "stage", "code", "country", "desired_country",
                                          "egress_id", "assignment_version", "helper_pid") if k in value}
            traffic = value.get("mesh", {}).get("traffic") if isinstance(value.get("mesh"), dict) else None
            if traffic:
                event["traffic"] = {k: traffic[k] for k in ("epoch", "rx_bytes", "tx_bytes") if k in traffic}
            event["elapsed_seconds"] = round(time.monotonic() - self.started, 2)
            self.events.put(event)

    def send(self, value):
        with self.input_lock:
            self.p.stdin.write((value if isinstance(value, str) else json.dumps(value)) + "\n")
            self.p.stdin.flush()

    def stop(self):
        try:
            self.send("stop")
            with self.input_lock:
                self.p.stdin.close()
        except (OSError, ValueError):
            pass

    def watchdog(self):
        # This timer is independent of probes, callbacks and the event loop.
        if not self.finished.wait(300):
            self.stop()

    def drain(self, terminal_ok=False):
        while not self.events.empty():
            event = self.events.get_nowait()
            self.history.append(event)
            if "helper_pid" in event:
                if self.helper_pid is not None and event["helper_pid"] != self.helper_pid:
                    raise RuntimeError("privileged_helper_changed")
                self.helper_pid = event["helper_pid"]
            if event.get("status") == "connected":
                self.latest = event
            if event.get("status") in ("failed", "expired") or event.get("code") == "network_cleanup_failed":
                raise RuntimeError("helper_reported_failure")
            if event.get("status") == "stopped":
                self.ended = True
                if not terminal_ok:
                    raise RuntimeError("helper_stopped_early")

    def wait(self, predicate, seconds=65):
        deadline = min(time.monotonic() + seconds, self.started + 275)
        while time.monotonic() < deadline:
            self.drain()
            if predicate():
                return
            if self.p.poll() is not None:
                raise RuntimeError("launcher_exited_early")
            time.sleep(0.15)
        raise TimeoutError("phase_deadline")

    def connected(self, country, version=0, different_egress=None, seconds=65):
        self.wait(lambda: self.latest is not None and self.latest.get("country") == country
                  and self.latest.get("desired_country") == country
                  and self.latest.get("assignment_version", 0) > version
                  and self.latest.get("egress_id") != different_egress
                  and bool(self.latest.get("traffic", {}).get("epoch"))
                  and self.helper_pid is not None, seconds=seconds)
        return dict(self.latest)

    def collect(self):
        start = len(self.history)
        self.send({"action": "collect"})
        self.wait(lambda: any(e.get("stage") == "garbage_collected" for e in self.history[start:]), seconds=10)


def verify_phase(session, name, country, pinned, inventory, records, previous=None, failover=False):
    phase = {"name": name, "country": country, "passed": False, "samples": []}
    records.append(phase)
    connected = session.connected(country, previous["assignment_version"] if previous else 0,
                                  previous["egress_id"] if failover else None,
                                  seconds=65 if previous else 120)
    phase.update({"name": name, "country": country, "egress_id": connected["egress_id"],
             "assignment_version": connected["assignment_version"],
             "helper_pid": session.helper_pid, "traffic_epoch": connected["traffic"]["epoch"],
             "connected_at_seconds": connected["elapsed_seconds"], "samples": []})
    if previous and (phase["helper_pid"] != previous["helper_pid"] or
                     phase["traffic_epoch"] != previous["traffic_epoch"]):
        raise RuntimeError("helper_or_system_tunnel_epoch_changed")
    session.collect()
    allowed = inventory.get(phase["egress_id"], [])
    if not allowed:
        raise RuntimeError("assigned_exit_absent_from_expected_inventory")
    for _ in range(2):
        session.drain()
        point = live.probes(pinned)
        trace = live.command(["/usr/bin/curl", "--noproxy", "*", "-4", "--max-time", "5", "-sS",
                              "https://www.cloudflare.com/cdn-cgi/trace"])
        match = re.search(r"^ip=(.+)$", trace["out"], re.M)
        outward = str(ipaddress.IPv4Address(match[1].strip())) if match else None
        point["outward_ip"] = outward
        point["trace_route_after"] = live.route()
        point["exit_inventory_match"] = trace["exit"] == 0 and outward in allowed
        point["elapsed_seconds"] = round(time.monotonic() - session.started, 2)
        if previous and point["route_before"] != previous["system_interface"]:
            raise RuntimeError("system_tunnel_interface_changed")
        phase["samples"].append(point)
        if (not point["same_system_tun"] or point["trace_route_after"] != point["route_before"]
                or not all(point["checks"].values()) or not point["exit_inventory_match"]):
            raise RuntimeError("independent_application_probe_failed")
        print(json.dumps({"phase": name, "helper_pid": session.helper_pid,
                          "assignment_version": phase["assignment_version"], "egress_id": phase["egress_id"],
                          "system_interface": point["route_before"], "checks": point["checks"],
                          "exit_inventory_match": point["exit_inventory_match"]}), flush=True)
        time.sleep(2)
    phase["system_interface"] = phase["samples"][0]["route_before"]
    phase["passed"] = True
    return phase


def cleanup_state(session, baseline, after, cleanup_timeout=False, terminal_failure=False):
    # Cancellation of an unapproved macOS authorization prompt has no root
    # lifecycle events. It is safe to disarm the operator plan once the launcher
    # exited cleanly and network state is unchanged, but is never a live pass.
    no_privileged_session = (session.helper_pid is None and not session.history
                             and session.latest is None)
    verified = (after == baseline and session.p.poll() == 0
                and (session.ended or no_privileged_session)
                and not session.reader.is_alive()
                and not cleanup_timeout and not terminal_failure)
    return verified, no_privileged_session


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for option in ("app", "credentials", "output", "failover-trigger", "failover-restore", "egress-inventory"):
        parser.add_argument("--" + option, type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() == 0:
        parser.error("run as the ordinary desktop user")
    if args.credentials.stat().st_mode & 0o077:
        parser.error("credentials must be private")
    link = json.loads(args.credentials.read_text())["link"]
    if not link.startswith("https://qntw.link/sub/"):
        parser.error("expected Quantway device link")
    inventory = json.loads(args.egress_inventory.read_text())
    if not inventory or any(not isinstance(values, list) for values in inventory.values()):
        parser.error("expected egress UUID to public IPv4 list mapping")
    pinned = socket.getaddrinfo("qntw.link", 443, socket.AF_INET, socket.SOCK_STREAM)[0][4][0]
    baseline = {"route": live.route(), "dns_keys": live.dns_keys()}
    report = {"uid": os.geteuid(), "baseline": baseline, "phases": [], "passed": False}
    session = Session(args.app / "Contents/Helpers/quantway-helper", link)
    trigger_egress = ""
    try:
        initial = verify_phase(session, "initial_PL", "PL", pinned, inventory, report["phases"])
        session.send({"action": "country", "country": "SE"})
        sweden = verify_phase(session, "country_SE", "SE", pinned, inventory, report["phases"], initial)
        session.send({"action": "country", "country": "PL"})
        poland = verify_phase(session, "country_PL", "PL", pinned, inventory, report["phases"], sweden)
        trigger_egress = poland["egress_id"]
        report["failover_trigger"] = callback(args.failover_trigger, trigger_egress)
        if report["failover_trigger"].get("armed") is not True:
            raise RuntimeError("failover_plan_not_armed")
        replacement = verify_phase(session, "automatic_PL_replacement", "PL", pinned, inventory, report["phases"], poland, True)
        if replacement["samples"][0]["outward_ip"] == poland["samples"][0]["outward_ip"]:
            raise RuntimeError("automatic_exit_kept_original_public_ip")
        report["transition_checks_passed"] = True
    except Exception as error:
        report["error_type"] = type(error).__name__
        report["error"] = str(error)
    finally:
        session.stop()
        try:
            session.p.wait(timeout=20)
        except subprocess.TimeoutExpired:
            report["cleanup_timeout"] = True
        session.reader.join(timeout=2)
        try:
            session.drain(terminal_ok=True)
        except RuntimeError as error:
            report["terminal_failure"] = str(error)
        session.finished.set()
        time.sleep(1)
        report["after"] = {"route": live.route(), "dns_keys": live.dns_keys()}
        report["cleanup_verified"], report["no_privileged_session_started"] = cleanup_state(
            session, baseline, report["after"], "cleanup_timeout" in report,
            "terminal_failure" in report)
        report["events"] = session.history
        report["elapsed_seconds"] = round(time.monotonic() - session.started, 2)
        # Restore only once the privileged owner has released routing and DNS;
        # the remote operator plan also has its independent restore watchdog.
        if report["cleanup_verified"]:
            try:
                report["failover_restore"] = callback(args.failover_restore, trigger_egress, timeout=180)
            except Exception as error:
                report["restore_error_type"] = type(error).__name__
        report["passed"] = (report.get("transition_checks_passed", False) and report["cleanup_verified"]
                            and report.get("failover_restore", {}).get("restored") is True)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"passed": report["passed"], "phases": len(report["phases"]),
                          "cleanup_verified": report["cleanup_verified"], "elapsed_seconds": report["elapsed_seconds"]}), flush=True)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
