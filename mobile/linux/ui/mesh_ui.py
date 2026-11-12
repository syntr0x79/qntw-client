"""Presentation of public mesh snapshots; no network access or route inference."""
from datetime import datetime, timezone
import re
from uuid import UUID


_NODE_NAME = re.compile(r"[A-Za-z0-9_-]{1,64}\Z")


def _instant(value):
    if not isinstance(value, str):
        return None
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return result if result.tzinfo else None
    except ValueError:
        return None


def _bytes(value):
    return value if isinstance(value, int) and not isinstance(value, bool) and 0 <= value < 2**64 else None


def format_traffic(value):
    if _bytes(value) is None:
        return "—"
    amount = float(value)
    for unit in ("Б", "КиБ", "МиБ", "ГиБ", "ТиБ"):
        if amount < 1024 or unit == "ТиБ":
            return (f"{int(amount)} {unit}" if unit == "Б" else f"{amount:.1f} {unit}".replace(".", ","))
        amount /= 1024


def mesh_snapshot(status, active, now=None):
    now = now or datetime.now(timezone.utc)
    state = status.get("status")
    mesh = status.get("mesh")
    mesh = mesh if isinstance(mesh, dict) else {}
    active = bool(active and state not in ("expired", "failed", "waiting") and
                  (state != "preparing" or mesh))
    result = {"active": active, "forwarding": active and state == "connected", "country": "", "egress_name": "", "egress_id": "", "nodes": [], "connected": 0,
              "complete": False, "fresh": False, "observation": "Нет активного соединения.",
              "rx_bytes": None, "tx_bytes": None, "traffic_sample": None}
    if not active:
        return result
    egress = mesh.get("egress")
    egress = egress if isinstance(egress, dict) else {}
    country = egress.get("country") or status.get("country")
    if isinstance(country, str) and re.fullmatch(r"[A-Z]{2}", country):
        result["country"] = country
    public_id = egress.get("id") or status.get("egress_id")
    try:
        node_id = UUID(public_id) if isinstance(public_id, str) else None
    except ValueError:
        node_id = None
    if node_id is not None and node_id.int:
        result["egress_id"] = str(node_id)
        result["egress_name"] = "Узел " + str(node_id)[:8]
    name = egress.get("name")
    if isinstance(name, str) and _NODE_NAME.fullmatch(name):
        result["egress_name"] = name
    observed = _instant(mesh.get("observed_at"))
    fresh = bool(observed and -5 <= (now - observed).total_seconds() <= 10 and
                 mesh.get("observation_state") == "ok")
    rows = mesh.get("ingresses")
    if isinstance(rows, list) and len(rows) <= 128:
        result["complete"] = True
        seen = set()
        for row in rows:
            if not isinstance(row, dict):
                continue
            name = row.get("name")
            if not isinstance(name, str) or not _NODE_NAME.fullmatch(name) or name in seen:
                continue
            seen.add(name)
            connected = row.get("connected") if fresh and isinstance(row.get("connected"), bool) else None
            result["nodes"].append({"name": name, "connected": connected})
        result["fresh"] = fresh
        result["observation"] = ("Снимок соединений: " + observed.astimezone().strftime("%H:%M:%S")
                                 if fresh else "Нет свежих данных о входах. Последнее состояние не считается текущим.")
    elif not mesh:
        # Older helpers only report confirmed connections. They cannot describe
        # missing peers, per-server traffic, or the inner server route.
        rows = status.get("connected_ingresses")
        for name in list(dict.fromkeys(item for item in (rows if isinstance(rows, list) else [])
                                       if isinstance(item, str) and _NODE_NAME.fullmatch(item)))[:128]:
            result["nodes"].append({"name": name, "connected": True})
        result["fresh"] = bool(result["nodes"])
        result["observation"] = "Последний статус помощника. Полный список входов и счётчики требуют обновлённого помощника."
    else:
        result["observation"] = "Нет свежих данных о входах. Последнее состояние не считается текущим."
    result["connected"] = sum(row["connected"] is True for row in result["nodes"])
    traffic = mesh.get("traffic")
    if isinstance(traffic, dict) and traffic.get("source") == "system_tun":
        sampled = _instant(traffic.get("sampled_at"))
        if sampled and -5 <= (now - sampled).total_seconds() <= 10:
            result["rx_bytes"], result["tx_bytes"] = _bytes(traffic.get("rx_bytes")), _bytes(traffic.get("tx_bytes"))
            epoch = traffic.get("epoch")
            if isinstance(epoch, str) and 0 < len(epoch) <= 128 and result["rx_bytes"] is not None and result["tx_bytes"] is not None:
                result["traffic_sample"] = (epoch, sampled.timestamp(), result["rx_bytes"], result["tx_bytes"])
    return result


def traffic_rates(previous, current):
    if previous is None or current is None or previous[0] != current[0]:
        return None, None
    elapsed = current[1] - previous[1]
    if elapsed <= 0 or elapsed > 10 or current[2] < previous[2] or current[3] < previous[3]:
        return None, None
    return int((current[2] - previous[2]) / elapsed), int((current[3] - previous[3]) / elapsed)
