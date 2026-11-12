"""Keep the audit transport correction limited to its reviewed generated method."""

import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from generate import TEMPLATES, generate_domain, guard_go_audit_read


AUDIT_PATH = "/api/v1/admin/audit-events"
GO_SOURCE = """package identity

func (c *Client) GetCsrf(ctx context.Context) (*http.Response, error) {
	return c.Client.Do(req)
}

func (c *Client) ListAuditEvents(ctx context.Context, params *ListAuditEventsParams) (*http.Response, error) {
	req, err := NewListAuditEventsRequest(c.Server, params)
	if err != nil { return nil, err }
	return c.Client.Do(req)
}
"""


class AuditReadCodegenTests(unittest.TestCase):
    def test_only_the_audit_dispatch_changes_and_reapplication_is_inert(self):
        expected = GO_SOURCE.rsplit("return c.Client.Do(req)", 1)
        expected = "return c.doAuditedRead(req)".join(expected)
        guarded = guard_go_audit_read(GO_SOURCE)
        self.assertEqual(guarded, expected)
        self.assertEqual(guard_go_audit_read(guarded), guarded)

    def test_missing_ambiguous_incomplete_or_changed_dispatch_fails_closed(self):
        for source in (
            GO_SOURCE.replace("ListAuditEvents(", "ListOtherEvents("),
            GO_SOURCE + GO_SOURCE,
            GO_SOURCE.rstrip()[:-1],
            GO_SOURCE.replace("c.Client.Do(req)", "c.OtherClient.Do(req)"),
            GO_SOURCE.replace("\treturn c.Client.Do(req)\n}", "\treturn c.Client.Do(req)\n\treturn c.Client.Do(req)\n}"),
        ):
            with self.subTest(source=source[-100:]):
                with self.assertRaisesRegex(ValueError, "generated|ListAuditEvents"):
                    guard_go_audit_read(source)

    def generate_fixture(self, root, domain, path, operation_id, forbid_tools=False):
        source = root / domain / "openapi.json"
        source.parent.mkdir()
        source.write_text(json.dumps({"paths": {path: {"get": {
            "operationId": operation_id, "x-audiences": ["admin"], "responses": {},
        }}}}))
        temp, go, python = (root / name for name in ("temp", "go", "python"))
        for directory in (temp, go, python):
            directory.mkdir()

        def tool(args, **kwargs):
            if forbid_tools:
                self.fail("an unreviewed audit operation reached a generator")
            if args[0] == "prepared-go":
                kwargs["stdout"].write(GO_SOURCE.encode())
            else:
                output = Path(args[args.index("--output-path") + 1])
                output.mkdir()
                (output / "__init__.py").write_text("")

        with patch("generate.subprocess.run", side_effect=tool) as tools:
            result = generate_domain(source, temp, go, python, "prepared-go", "prepared-python")
        return result, go / domain, tools.call_count

    def test_generation_installs_the_reviewed_helper_for_the_public_audit_read(self):
        with tempfile.TemporaryDirectory() as scratch:
            result, output, calls = self.generate_fixture(Path(scratch), "identity", AUDIT_PATH, "listAuditEvents")
            self.assertTrue(result)
            self.assertEqual(calls, 2)
            self.assertEqual((output / "client.gen.go").read_text(), guard_go_audit_read(GO_SOURCE))
            self.assertEqual((output / "audit_read.gen.go").read_bytes(),
                             (TEMPLATES / "audit_read.go.template").read_bytes())

    def test_other_contracts_do_not_get_the_helper_or_a_changed_executor(self):
        with tempfile.TemporaryDirectory() as scratch:
            result, output, _ = self.generate_fixture(Path(scratch), "models", "/models", "listModels")
            self.assertTrue(result)
            self.assertEqual((output / "client.gen.go").read_text(), GO_SOURCE)
            self.assertFalse((output / "audit_read.gen.go").exists())

    def test_route_move_or_rename_requires_review_before_invoking_generators(self):
        for domain, path, operation in (
            ("identity", AUDIT_PATH, "renamedAuditRead"),
            ("other", AUDIT_PATH, "listAuditEvents"),
            ("identity", "/moved", "listAuditEvents"),
        ):
            with self.subTest(domain=domain, path=path), tempfile.TemporaryDirectory() as scratch:
                with self.assertRaisesRegex(ValueError, "moved or was renamed"):
                    self.generate_fixture(Path(scratch), domain, path, operation, forbid_tools=True)
