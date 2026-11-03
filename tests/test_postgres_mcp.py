import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("postgres_mcp", Path(__file__).resolve().parents[1] / "examples/postgres-mcp.py")
server = importlib.util.module_from_spec(spec)
spec.loader.exec_module(server)


class ReadOnlySqlTests(unittest.TestCase):
    def test_accepts_select(self):
        self.assertEqual(server.readonly_query(" SELECT id FROM public.items "), "SELECT id FROM public.items")

    def test_rejects_mutation_and_multiple_statements(self):
        for query in ("DELETE FROM items", "SELECT 1; DROP TABLE items", "SELECT 1 --comment", "SELECT 1 INTO other", "SELECT * FROM items FOR UPDATE", "SELECT pg_sleep(20)"):
            with self.subTest(query=query), self.assertRaises(ValueError):
                server.readonly_query(query)

    def test_query_length(self):
        with self.assertRaises(ValueError):
            server.readonly_query("SELECT " + " " * server.MAX_SQL_CHARS)

    def test_unknown_tool_rejected(self):
        with self.assertRaises(ValueError):
            server.call_tool("not-a-tool", {})


if __name__ == "__main__":
    unittest.main()
