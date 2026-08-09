import unittest

from scripts.updatePrice import collect_price_entries


class CollectPriceEntriesTest(unittest.TestCase):
    def test_later_provider_replaces_duplicate_model(self):
        raw_price = {
            "alibaba": {
                "models": {
                    "first": {
                        "id": "glm-5.2",
                        "cost": {"input": 1.4, "output": 4.4, "cache_read": 0.28},
                    }
                }
            },
            "zhipuai": {
                "models": {
                    "second": {
                        "id": "GLM-5.2",
                        "cost": {"input": 1.4, "output": 4.4, "cache_read": 0.26},
                    }
                }
            },
        }

        entries, _, messages = collect_price_entries(raw_price)

        self.assertEqual(0.26, entries["glm-5.2"]["cache_read"])
        self.assertEqual(1, len(entries))
        self.assertTrue(any("replaced alibaba model with zhipuai model" in message for message in messages))

    def test_explicit_model_wins_over_generated_alias(self):
        raw_price = {
            "anthropic": {
                "models": {
                    "explicit": {
                        "id": "claude-4.5-opus",
                        "cost": {"input": 10, "output": 20},
                    },
                    "alias-source": {
                        "id": "claude-opus-4-5",
                        "cost": {"input": 1, "output": 2},
                    },
                }
            }
        }

        entries, _, messages = collect_price_entries(raw_price)

        self.assertEqual(10, entries["claude-4.5-opus"]["input"])
        self.assertTrue(any("kept model" in message and "skipped alias" in message for message in messages))


if __name__ == "__main__":
    unittest.main()
