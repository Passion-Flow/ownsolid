import sys

REASON = "OPS_OLD_EPOCH_CLOSED: Await the coordinator-approved new epoch."


def acquire(_path):
    raise ValueError(REASON)


def check():
    raise ValueError(REASON)


def run(_command):
    raise ValueError(REASON)


if __name__ == "__main__":
    print(REASON, file=sys.stderr)
    raise SystemExit(75)
