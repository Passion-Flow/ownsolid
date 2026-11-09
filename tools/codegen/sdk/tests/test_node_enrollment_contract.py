"""Keep the Nodes Kubernetes enrollment projection aligned with agent v1."""

import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[4]
PROFILE = "kubernetes_node"


def contract_schemas(path):
    return json.loads((ROOT / path).read_text(encoding="utf-8"))["components"]["schemas"]


def kubernetes_branch(schema):
    branch, = (candidate for candidate in schema["oneOf"]
               if candidate["properties"].get("execution_profile", {}).get("const") == PROFILE)
    return branch


class NodeEnrollmentContractTests(unittest.TestCase):
    def test_kubernetes_requests_match_the_agent_handshake(self):
        nodes = contract_schemas("api/openapi/v1/nodes/openapi.json")
        agent = contract_schemas("api/agent/v1/schema.json")
        handshake = kubernetes_branch(agent["Handshake"])
        self.assertEqual(kubernetes_branch(nodes["Handshake"]), handshake)
        enrollment = kubernetes_branch(nodes["Enrollment"])
        self.assertFalse(enrollment["additionalProperties"])
        self.assertEqual(set(enrollment["required"]), set(handshake["required"]) | {"join_token"})
        self.assertEqual(set(enrollment["properties"]),
                         set(handshake["properties"]) | {"join_token", "certificate_request"})
        for name, schema in handshake["properties"].items():
            self.assertEqual(enrollment["properties"][name], schema)
        self.assertIn(handshake["properties"]["protocol_version"]["const"],
                      nodes["Enrolled"]["properties"]["protocol_version"]["enum"])

    def test_kubernetes_profile_survives_public_node_and_token_reads(self):
        nodes = contract_schemas("api/openapi/v1/nodes/openapi.json")
        for name in ("CreateJoinToken", "MintedToken", "JoinToken", "Node"):
            with self.subTest(schema=name):
                self.assertIn(PROFILE, nodes[name]["properties"]["execution_profile"]["enum"])
        self.assertNotIn("execution_profile", nodes["CreateJoinToken"]["required"])
        handshake = kubernetes_branch(nodes["Handshake"])
        capabilities = {item["const"] for item in handshake["properties"]["capabilities"]["prefixItems"]}
        observed = nodes["Node"]["properties"]["capabilities"]["items"]["enum"]
        self.assertTrue(capabilities <= set(observed))
        self.assertEqual(len(observed), len(set(observed)))
