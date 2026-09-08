import unittest

from release_infra.retry import retry


class RetryTest(unittest.TestCase):
    def test_transient_failure_retries_with_budget(self):
        calls = []

        def operation():
            calls.append(1)
            if len(calls) < 3:
                raise OSError("timeout")
            return "ok"

        self.assertEqual(retry(operation, lambda exc: "timeout" in str(exc), (0, 0), sleep=lambda _: None), "ok")
        self.assertEqual(len(calls), 3)

    def test_deterministic_failure_does_not_retry(self):
        calls = []

        def operation():
            calls.append(1)
            raise ValueError("checksum mismatch")

        with self.assertRaises(ValueError):
            retry(operation, lambda exc: False, (0, 0), sleep=lambda _: None)
        self.assertEqual(len(calls), 1)


if __name__ == "__main__":
    unittest.main()
