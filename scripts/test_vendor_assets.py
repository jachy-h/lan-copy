import base64
import hashlib
import io
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch
from vendor_assets import check, upstream_files


class VendorAssetsTests(unittest.TestCase):
    def test_checksum_missing_and_extra_files_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            vendor = root / 'web/vendor'
            vendor.mkdir(parents=True)
            file = vendor / 'library.js'
            file.write_bytes(b'original')
            manifest = {'projectFiles': [], 'packages': [{'files': [{'path': 'library.js', 'sha256': hashlib.sha256(b'original').hexdigest()}]}]}
            check(root, manifest)
            file.write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                check(root, manifest)
            file.unlink()
            with self.assertRaises(FileNotFoundError):
                check(root, manifest)
            file.write_bytes(b'original')
            (vendor / 'extra.js').write_bytes(b'extra')
            with self.assertRaisesRegex(ValueError, 'file list'):
                check(root, manifest)

    def test_untrusted_archive_is_rejected_before_extraction(self):
        package = {'name': 'test', 'url': 'https://example.test/package.tgz', 'integrity': 'sha512-' + base64.b64encode(hashlib.sha512(b'expected').digest()).decode()}
        with patch('urllib.request.urlopen', return_value=io.BytesIO(b'tampered')):
            with self.assertRaisesRegex(ValueError, 'integrity mismatch'):
                upstream_files(package)
