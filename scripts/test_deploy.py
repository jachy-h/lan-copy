import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from deploy import deploy


def connection(status, data):
    conn = Mock()
    response = conn.getresponse.return_value
    response.status = status
    response.read.return_value = json.dumps(data).encode()
    return conn


class DeployTests(unittest.TestCase):
    def run_deploy(self, connections):
        with tempfile.TemporaryDirectory() as directory:
            package = Path(directory) / "package.zip"
            package.write_bytes(b"signed archive")
            with patch("deploy.connect", side_effect=connections), patch("deploy.time.sleep"), patch("sys.stdout", new_callable=io.StringIO):
                deploy("https://example.test", package, "secret")

    def test_waits_for_matching_process(self):
        upload = connection(202, {"version": "1.2.3", "updateID": "new"})
        old = connection(200, {"version": "1.2.3", "updateID": "old"})
        ready = connection(200, {"version": "1.2.3", "updateID": "new"})
        self.run_deploy([upload, old, ready])
        args = upload.request.call_args.args
        self.assertEqual(args[0:2], ("POST", "/api/deploy"))
        self.assertEqual(args[3]["Authorization"], "Bearer secret")
        self.assertEqual(args[3]["Content-Length"], "14")
        self.assertEqual(ready.request.call_args.args, ("GET", "/api/app"))
        upload.close.assert_called_once()

    def test_rejection_and_redirect_are_not_retried(self):
        for code in (401, 400, 503, 307):
            with self.subTest(code=code), self.assertRaisesRegex(ValueError, str(code)):
                self.run_deploy([connection(code, {"error": "rejected"})])

    def test_rollback_is_failure(self):
        with self.assertRaisesRegex(ValueError, "恢复旧版本"):
            self.run_deploy([
                connection(202, {"version": "1.2.3", "updateID": "new"}),
                connection(200, {"version": "1.2.2", "updateID": "rollback-new"}),
            ])

    def test_invalid_configuration_before_upload(self):
        for url, token in [("ftp://host", "secret"), ("http://host", ""), ("https://user:pass@host", "secret")]:
            with self.subTest(url=url), self.assertRaises(ValueError):
                deploy(url, Path("missing.zip"), token)
