import unittest
from datetime import datetime, timedelta, timezone

from mesh_ui import format_traffic, mesh_snapshot, traffic_rates


class MeshPresentationTests(unittest.TestCase):
    def setUp(self):
        self.now = datetime(2026, 11, 1, tzinfo=timezone.utc)
        self.event = {"status": "connected", "country": "PL", "desired_country": "US",
                      "connected_ingresses": ["old-peer"], "mesh": {
                          "observation_state": "ok", "observed_at": self.now.isoformat(),
                          "ingresses": [{"name": "in-pl", "connected": True}, {"name": "in-us", "connected": False}],
                          "egress": {"country": "PL", "id": "e87f3ca3-b9b7-4d98-90ed-0d07ce9f1427"},
                          "traffic": {"source": "system_tun", "epoch": "session-one", "sampled_at": self.now.isoformat(),
                                      "rx_bytes": 4096, "tx_bytes": 512}}}

    def view(self):
        return mesh_snapshot(self.event, True, self.now)

    def test_confirmed_and_disconnected_are_distinct_and_requested_country_is_not_current(self):
        v = self.view()
        self.assertEqual(v["country"], "PL")
        self.assertEqual(v["egress_name"], "Узел e87f3ca3")
        self.assertEqual(v["connected"], 1)
        self.assertEqual([p["connected"] for p in v["nodes"]], [True, False])
        self.assertTrue(v["fresh"])
        self.assertEqual(v["rx_bytes"], 4096)

    def test_query_failure_never_falls_back_to_old_green_names(self):
        self.event["mesh"]["observation_state"] = "unavailable"
        self.event["mesh"]["ingresses"] = [{"name": "in-pl"}, {"name": "in-us"}]
        v = self.view()
        self.assertFalse(v["fresh"])
        self.assertEqual([p["connected"] for p in v["nodes"]], [None, None])
        self.assertEqual(v["connected"], 0)

    def test_stale_or_future_observation_does_not_claim_live_connections(self):
        for seconds in (-20, 20):
            self.event["mesh"]["observed_at"] = (self.now + timedelta(seconds=seconds)).isoformat()
            self.assertTrue(all(p["connected"] is None for p in self.view()["nodes"]))

    def test_stopped_expired_or_failed_clears_all_live_data(self):
        for state in ("expired", "failed", "waiting"):
            self.event["status"] = state
            v = self.view()
            self.assertEqual(v["nodes"], [])
            self.assertIsNone(v["rx_bytes"])
        self.assertEqual(mesh_snapshot(self.event, False, self.now)["nodes"], [])

    def test_exit_health_check_keeps_observed_peers_without_claiming_forwarding(self):
        self.event["status"] = "preparing"
        self.assertEqual(self.view()["connected"], 1)
        self.assertFalse(self.view()["forwarding"])
        del self.event["mesh"]
        self.assertEqual(self.view()["nodes"], [])

    def test_reconnecting_does_not_claim_forwarding(self):
        self.event["status"] = "reconnecting"
        self.assertTrue(self.view()["active"])
        self.assertFalse(self.view()["forwarding"])

    def test_old_helper_has_only_reported_connected_names_and_no_full_roster(self):
        del self.event["mesh"]
        v = self.view()
        self.assertEqual(v["nodes"], [{"name": "old-peer", "connected": True}])
        self.assertFalse(v["complete"])
        self.assertIsNone(v["rx_bytes"])

    def test_untrusted_names_and_invalid_counters_are_not_presented(self):
        self.event["mesh"]["ingresses"] += [{"name": "in-pl", "connected": True}, {"name": "https://secret.invalid/key", "connected": True}]
        self.event["mesh"]["traffic"]["rx_bytes"] = True
        self.event["mesh"]["traffic"]["tx_bytes"] = -1
        v = self.view()
        self.assertEqual(len(v["nodes"]), 2)
        self.assertIsNone(v["rx_bytes"])
        self.assertIsNone(v["tx_bytes"])

    def test_traffic_from_old_or_unknown_source_is_not_shown(self):
        traffic = self.event["mesh"]["traffic"]
        traffic["sampled_at"] = (self.now - timedelta(seconds=20)).isoformat()
        self.assertIsNone(self.view()["rx_bytes"])
        traffic["sampled_at"] = self.now.isoformat()
        traffic["source"] = "unknown"
        self.assertIsNone(self.view()["rx_bytes"])

    def test_rates_never_cross_epoch_reset_or_stale_sample(self):
        previous = ("one", 10, 100, 200)
        self.assertEqual(traffic_rates(previous, ("one", 12, 300, 220)), (100, 10))
        for current in (("two", 12, 300, 220), ("one", 10, 300, 220), ("one", 30, 300, 220), ("one", 12, 1, 220)):
            self.assertEqual(traffic_rates(previous, current), (None, None))
        self.assertEqual(format_traffic(1024), "1,0 КиБ")
        self.assertEqual(format_traffic(None), "—")


if __name__ == "__main__":
    unittest.main()
