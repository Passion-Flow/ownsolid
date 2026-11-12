"""Count fixture TCP states in the caller's Linux network namespace."""
import argparse
import json
from pathlib import Path
import socket
import time

# Stable Linux TCP state values; preserve unrecognized values in the observation.
STATES = {"01": "established", "02": "syn_sent", "03": "syn_received",
          "04": "fin_wait_1", "05": "fin_wait_2", "06": "time_wait",
          "07": "closed", "08": "close_wait", "09": "last_ack",
          "0A": "listen", "0B": "closing", "0C": "new_syn_received"}


def observe(port):
    counts = {}
    for name in ("tcp", "tcp6"):
        path = Path("/proc/self/net") / name
        if not path.exists() and name == "tcp6":
            continue
        for line in path.read_text().splitlines()[1:]:
            fields = line.split()
            if len(fields) < 4:
                raise ValueError("incomplete kernel TCP observation")
            if int(fields[1].rsplit(":", 1)[1], 16) != port:
                continue
            state = STATES.get(fields[3].upper(), "unknown_" + fields[3])
            counts[state] = counts.get(state, 0) + 1
    return counts


def prototype():
    if not Path("/proc/self/net/tcp").is_file():
        raise RuntimeError("the socket prototype requires its isolated Linux fixture")
    with socket.socket() as server:
        server.bind(("127.0.0.1", 0))
        server.listen(3)
        port = server.getsockname()[1]
        initial = observe(port)
        assert initial.get("listen") == 1 and initial.get("established", 0) == 0, initial
        clients, accepted = [], []
        try:
            for _ in range(3):
                client = socket.create_connection(("127.0.0.1", port), timeout=2)
                clients.append(client)
                accepted.append(server.accept()[0])
            opened = observe(port)
            assert opened.get("established") == 3, opened
        finally:
            for connection in clients + accepted:
                connection.close()
        deadline = time.monotonic() + 2
        while True:
            closed = observe(port)
            if closed.get("established", 0) == 0:
                break
            assert time.monotonic() < deadline, closed
            time.sleep(.02)
        print(json.dumps({"initial": initial, "three_open": opened,
                          "after_close": closed, "physical_socket_test": "passed"}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--port", type=int)
    arguments = parser.parse_args()
    if arguments.self_test:
        prototype()
    else:
        if arguments.port is None or not 1 <= arguments.port <= 65535:
            parser.error("a serving port in 1..65535 is required")
        print(json.dumps(observe(arguments.port)))
