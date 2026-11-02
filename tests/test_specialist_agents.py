"""Offline safety and behavior checks for deterministic specialist agents."""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("demo_specialists", Path(__file__).resolve().parents[1] / "examples/demo-server.py")
demo = importlib.util.module_from_spec(spec)
spec.loader.exec_module(demo)


class ResearchSafetyTests(unittest.TestCase):
    def test_url_allowlist_and_https(self):
        for url in ("http://www.iana.org/", "https://evil.example/", "https://127.0.0.1/",
                    "https://www.python.org:444/", "https://user@www.iana.org/"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                demo.validate_research_url(url)

    @patch.object(demo.socket, "getaddrinfo", return_value=[(None, None, None, None, ("127.0.0.1", 443))])
    def test_private_dns_answer_is_rejected(self, _):
        with self.assertRaisesRegex(ValueError, "non-public"):
            demo.validate_research_url("https://www.iana.org/")

    @patch.object(demo, "research")
    def test_allowed_calls_bounded_and_no_network_in_tests(self, research):
        research.return_value = {"text": "ok"}
        self.assertEqual(demo.call_tool("research", {"url": "https://www.iana.org/"}), {"text": "ok"})
        research.assert_called_once()
        with self.assertRaises(ValueError):
            demo.call_tool("research", {"url": "https://www.iana.org/", "extra": True})


class DataAnalystTests(unittest.TestCase):
    def test_numeric_and_categorical_summary(self):
        result = demo.data_summary("score,team\n2,a\n4,b\n6,a\n", "csv")
        self.assertEqual(result["rows"], 3)
        self.assertEqual(result["column_summaries"]["score"]["mean"], 4)
        self.assertEqual(result["column_summaries"]["score"]["median"], 4)
        self.assertEqual(result["column_summaries"]["team"]["top_values"][0], ("a", 2))

    def test_json_and_invalid_inputs(self):
        result = demo.data_summary('[{"x":1},{"x":3}]', "json")
        self.assertEqual(result["column_summaries"]["x"]["max"], 3)
        for data, fmt in (("{}", "json"), ("x\n1,2", "csv"), ("[]", "json"), ("x", "xml"), ("x" * 65537, "csv")):
            with self.subTest(fmt=fmt, data=data[:20]), self.assertRaises(ValueError):
                demo.data_summary(data, fmt)

    def test_tool_strict_arguments(self):
        result = demo.call_tool("data_summary", {"format": "csv", "data": "x\n1\n"})
        self.assertEqual(result["rows"], 1)
        with self.assertRaises(ValueError):
            demo.call_tool("data_summary", {"format": "csv", "data": "x\n1\n", "extra": 1})


class IssueTriageTests(unittest.TestCase):
    def test_fixed_team_and_explicit_heuristic(self):
        result = demo.triage_issue("Security vulnerability with leaked credential")
        self.assertEqual(result["team"], "security")
        self.assertGreaterEqual(result["confidence"], 0.5)
        self.assertTrue(result["heuristic"])
        self.assertIn("not authorization", result["note"])
        self.assertIn(result["team"], demo.TRIAGE_TEAMS)

    def test_fallback_and_validation(self):
        result = demo.triage_issue("Unclear problem")
        self.assertEqual(result["team"], "product")
        self.assertEqual(result["confidence"], 0.25)
        for issue in ("", "  ", "x" * 10001):
            with self.subTest(issue=issue[:10]), self.assertRaises(ValueError):
                demo.triage_issue(issue)


if __name__ == "__main__":
    unittest.main()
