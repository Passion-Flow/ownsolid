import hashlib


def receive_payload(read_piece, wait_readable, now, deadline, expected_size, expected_sha256, receipt):
    """Read the exact frozen input and EOF within the existing admission deadline."""
    started = now()
    received = bytearray()
    digest = hashlib.sha256()
    eof = False
    try:
        while True:
            remaining = deadline - now()
            if remaining <= 0 or not wait_readable(remaining):
                raise TimeoutError("PAYLOAD_ADMISSION_DEADLINE_EXCEEDED")
            try:
                data = read_piece(min(65536, expected_size + 1 - len(received)))
            except BlockingIOError:
                continue
            if not data:
                eof = True
                break
            received.extend(data)
            digest.update(data)
            if len(received) > expected_size:
                raise ValueError("PAYLOAD_BYTE_COUNT_MISMATCH")
        if len(received) != expected_size:
            raise ValueError("PAYLOAD_BYTE_COUNT_MISMATCH")
        if digest.hexdigest() != expected_sha256:
            raise ValueError("PAYLOAD_DIGEST_MISMATCH")
        return bytes(received)
    finally:
        receipt.update(
            expected_bytes=expected_size,
            expected_sha256=expected_sha256,
            received_bytes=len(received),
            received_sha256=digest.hexdigest(),
            eof_observed=eof,
            elapsed_seconds=now() - started,
        )


def classify_opportunity(ssh_exit, stage_events, remote_receipt=None):
    """Keep confirmed remote entry separate from missing SQL/cleanup evidence."""
    if isinstance(remote_receipt, dict):
        return remote_receipt.get("state", "INCOMPLETE_REMOTE_RECEIPT")
    entered = any(item.get("stage") == "remote_entry_confirmed" for item in stage_events)
    if entered:
        return "FAILED_AFTER_REMOTE_ENTRY" if ssh_exit else "INCOMPLETE_AFTER_REMOTE_ENTRY"
    return "ENTRY_UNCONFIRMED_CONNECTION_FAILED" if ssh_exit else "INCOMPLETE_ENTRY_UNCONFIRMED"
