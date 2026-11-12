import os
from pathlib import Path
import tempfile
import unittest

import deploy_token


class DeployTokenTest(unittest.TestCase):
    def test_generates_private_token_and_rotates_explicitly(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".config" / "deploy.token"
            first = deploy_token.generate(path)
            self.assertEqual(len(first), 64)
            int(first, 16)
            self.assertEqual(path.read_text().strip(), first)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                deploy_token.generate(path)
            second = deploy_token.generate(path, rotate=True)
            self.assertNotEqual(first, second)
            self.assertEqual(path.read_text().strip(), second)

    def test_removes_partial_file_when_write_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "deploy.token"
            original_fdopen = deploy_token.os.fdopen

            def failing_fdopen(fd, mode):
                os.close(fd)
                raise OSError("write failed")

            deploy_token.os.fdopen = failing_fdopen
            try:
                with self.assertRaises(OSError):
                    deploy_token.generate(path)
            finally:
                deploy_token.os.fdopen = original_fdopen
            self.assertFalse(path.exists())


if __name__ == "__main__":
    unittest.main()
