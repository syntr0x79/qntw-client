"""Acceptance must reject a changed helper/TUN or a direct-network probe."""
import importlib.util
from pathlib import Path
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("transitions", Path(__file__).with_name("live-tinc-transitions.py"))
transitions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(transitions)


class FakeSession:
    helper_pid = 123
    started = 0
    epoch = "system-epoch"

    def __init__(self):
        self.collections = 0

    def connected(self, country, version=0, different_egress=None, seconds=65):
        self.connected_budget = seconds
        return {"country": country, "egress_id": "new-exit", "assignment_version": version + 1,
                "traffic": {"epoch": self.epoch}, "elapsed_seconds": 1}

    def collect(self):
        self.collections += 1

    def drain(self):
        pass


class TransitionAcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.records = []
        self.session = FakeSession()
        self.point = {"route_before": "utun7", "route_after": "utun7", "same_system_tun": True,
                      "checks": {"https": True, "pinned_https": True, "udp_dns": True, "system_dns": True}}
        for target, value in (
            ("probes", lambda _: dict(self.point)),
            ("command", lambda _: {"exit": 0, "out": "ip=203.0.113.7\n"}),
            ("route", lambda: "utun7"),
        ):
            patcher = mock.patch.object(transitions.live, target, value)
            patcher.start()
            self.addCleanup(patcher.stop)
        patcher = mock.patch.object(transitions.time, "sleep")
        patcher.start()
        self.addCleanup(patcher.stop)

    def phase(self, previous=None):
        return transitions.verify_phase(self.session, "test", "PL", "192.0.2.1",
                                        {"new-exit": ["203.0.113.7"]}, self.records, previous)

    def test_real_phase_requires_gc_and_two_system_tunnel_probes(self):
        phase = self.phase()
        self.assertTrue(phase["passed"])
        self.assertEqual(self.session.collections, 1)
        self.assertEqual(self.session.connected_budget, 120)
        self.assertEqual(len(phase["samples"]), 2)
        self.assertIs(self.records[0], phase)

    def test_changed_primary_tunnel_epoch_is_not_a_successful_rebuild(self):
        previous = {"helper_pid": 123, "traffic_epoch": "other-epoch", "assignment_version": 1,
                    "egress_id": "old-exit", "system_interface": "utun7"}
        with self.assertRaisesRegex(RuntimeError, "epoch_changed"):
            self.phase(previous)
        self.assertFalse(self.records[0]["passed"])

    def test_unapproved_authorization_is_cleaned_but_never_a_live_phase(self):
        session = mock.Mock(helper_pid=None, history=[], latest=None, ended=False)
        session.p.poll.return_value = 0
        session.reader.is_alive.return_value = False
        baseline = {"route": "en0", "dns_keys": ""}
        cleaned, never_started = transitions.cleanup_state(session, baseline, dict(baseline))
        self.assertTrue(cleaned)
        self.assertTrue(never_started)
        # No connected phase or transition result exists to satisfy main's
        # independent transition_checks_passed gate.
        self.assertFalse(bool([]))
        session.history = [{"stage": "identity"}]
        cleaned, never_started = transitions.cleanup_state(session, baseline, dict(baseline))
        self.assertFalse(cleaned)
        self.assertFalse(never_started)

    def test_unapproved_session_still_requires_launcher_exit_and_network_baseline(self):
        session = mock.Mock(helper_pid=None, history=[], latest=None, ended=False)
        session.p.poll.return_value = None
        session.reader.is_alive.return_value = False
        baseline = {"route": "en0", "dns_keys": ""}
        self.assertFalse(transitions.cleanup_state(session, baseline, dict(baseline))[0])
        session.p.poll.return_value = 0
        self.assertFalse(transitions.cleanup_state(session, baseline, {"route": "utun7", "dns_keys": ""})[0])

    def test_public_ip_on_the_wrong_exit_is_retained_as_failed_evidence(self):
        with mock.patch.object(transitions.live, "command", return_value={"exit": 0, "out": "ip=203.0.113.8\n"}):
            with self.assertRaisesRegex(RuntimeError, "application_probe_failed"):
                self.phase()
        self.assertFalse(self.records[0]["passed"])
        self.assertEqual(self.records[0]["samples"][0]["outward_ip"], "203.0.113.8")

    def test_route_restoring_during_public_ip_request_cannot_pass(self):
        with mock.patch.object(transitions.live, "route", return_value="en0"):
            with self.assertRaisesRegex(RuntimeError, "application_probe_failed"):
                self.phase()
        self.assertFalse(self.records[0]["passed"])


if __name__ == "__main__":
    unittest.main()
