"""Check the gateway attachment operations in the public SDK projection."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, TEMPLATES, project_contract


class GatewayDownloadContractTests(unittest.TestCase):
    def test_export_operations_follow_their_json_history(self):
        source = CONTRACTS / "distribution" / "openapi.json"
        document = project_contract(json.loads(source.read_text(encoding="utf-8")))
        prefix = "/api/v1/sites/{site_id}/cells/{cell_id}"
        for audience, route in {
            "admin": prefix + "/admin/distribution/workspaces/{workspace_id}/requests",
            "portal": prefix + "/workspaces/{workspace_id}/distribution/requests",
        }.items():
            with self.subTest(audience=audience):
                history = document["paths"][route]
                export = document["paths"][route + "/export"]
                operation = export["get"]
                self.assertEqual(operation["operationId"], audience + "ExportGatewayUsageCSV")
                self.assertEqual(operation["x-audiences"], [audience])
                self.assertEqual(operation["security"], [{audience + "Session": []}])
                self.assertEqual(export["parameters"], history["parameters"])
                parameters = {p["name"]: p for p in operation["parameters"]}
                expected = {p["name"]: p for p in history["get"]["parameters"]}
                self.assertEqual(set(parameters), set(expected))
                for name, parameter in parameters.items():
                    self.assertEqual(parameter["schema"], expected[name]["schema"])
                self.assertEqual(set(operation["responses"]), {"200", "400", "401", "403", "404", "503", "default"})
                self.assertEqual(operation["responses"]["200"], {"$ref": "#/components/responses/GatewayUsageCSVDownload"})
                for status in ("400", "401", "403", "404", "503", "default"):
                    self.assertEqual(operation["responses"][status], {"$ref": "#/components/responses/GatewayUsageCSVProblem"})

        responses = document["components"]["responses"]
        self.assertEqual(responses["GatewayUsageCSVDownload"]["content"], {"text/csv": {"schema": {"type": "string", "format": "binary"}}})
        self.assertEqual(set(responses["GatewayUsageCSVDownload"]["headers"]), {
            "Content-Disposition", "Content-Length", "X-Artifact-SHA256", "Cache-Control", "X-Request-ID",
        })
        self.assertEqual(responses["GatewayUsageCSVProblem"]["content"], {
            "application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}},
        })
        config = json.loads((TEMPLATES / "python-config.json").read_text(encoding="utf-8"))
        self.assertEqual(config["content_type_overrides"]["text/csv"], "application/octet-stream")

    def test_alias_ca_download_uses_the_authorized_alias_path(self):
        source = CONTRACTS / "distribution" / "openapi.json"
        document = project_contract(json.loads(source.read_text(encoding="utf-8")))
        prefix = "/api/v1/sites/{site_id}/cells/{cell_id}"
        for audience, route in {
            "admin": prefix + "/admin/distribution/workspaces/{workspace_id}/aliases/{resource_id}",
            "portal": prefix + "/workspaces/{workspace_id}/distribution/aliases/{resource_id}",
        }.items():
            with self.subTest(audience=audience):
                detail = document["paths"][route]
                export = document["paths"][route + "/ca"]
                operation = export["get"]
                self.assertEqual(operation["operationId"], audience + "DownloadGatewayAliasCA")
                self.assertEqual(operation["security"], detail["get"]["security"])
                self.assertEqual(export["parameters"], detail["parameters"])
                self.assertEqual(operation["parameters"], detail["get"]["parameters"])
                self.assertTrue(all(p["in"] == "path" for p in operation["parameters"]))
                self.assertEqual(operation["responses"]["200"], {"$ref": "#/components/responses/GatewayAliasCADownload"})
                self.assertIn("GATEWAY_AUTHORITY_UNAVAILABLE", operation["x-error-codes"]["503"])

        components = document["components"]
        response = components["responses"]["GatewayAliasCADownload"]
        self.assertEqual(response["content"], {"application/octet-stream": {"schema": {"type": "string", "format": "binary"}}})
        self.assertEqual(response["headers"]["Content-Disposition"]["schema"]["const"], "attachment; filename=model-gateway-ca.pem")
        codes = components["schemas"]["Problem"]["properties"]["code"]["enum"]
        self.assertIn("GATEWAY_AUTHORITY_UNAVAILABLE", codes)
