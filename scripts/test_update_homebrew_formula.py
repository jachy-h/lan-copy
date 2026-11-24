import tempfile
import unittest
from pathlib import Path

from update_homebrew_formula import parse_checksums, update_formula, updated_formula

ASSET = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
OLD_SHA = "fe6cb38a213708405f9051c78f17bbca426053397003862953757eee001c9f37"
NEW_SHA = "1" * 64
FORMULA = f'''class Lancopy < Formula
  desc "Zero-dependency LAN file and text sharing tool"
  homepage "https://github.com/jachy-h/lan-copy"
  url "https://github.com/jachy-h/lan-copy/releases/download/v0.8.1/{ASSET}"
  sha256 "{OLD_SHA}"
  license "MIT"

  def install
    bin.install "lan-copy" => "lancopy"
  end

  test do
    assert_match "lancopy", shell_output("#{{bin}}/lancopy -h 2>&1", 2)
  end
end
'''


class ParseChecksumsTests(unittest.TestCase):
    def test_single_asset(self):
        text = f'{NEW_SHA}  Lan-Copy-Windows-amd64.zip\n{NEW_SHA}  {ASSET}\n'
        self.assertEqual(parse_checksums(text, ASSET), NEW_SHA)

    def test_only_the_requested_asset_counts(self):
        text = f'{"a" * 64}  Lan-Copy-Windows-amd64.zip\n'
        with self.assertRaises(ValueError):
            parse_checksums(text, ASSET)

    def test_missing_asset(self):
        with self.assertRaises(ValueError) as caught:
            parse_checksums(f'{NEW_SHA}  other.tar.gz\n', ASSET)
        self.assertIn(ASSET, str(caught.exception))

    def test_duplicate_asset(self):
        text = f'{NEW_SHA}  {ASSET}\n{"b" * 64}  {ASSET}\n'
        with self.assertRaises(ValueError) as caught:
            parse_checksums(text, ASSET)
        self.assertIn("more than once", str(caught.exception))

    def test_binary_marker_and_uppercase(self):
        text = f'{"C" * 64} *{ASSET}\n'
        self.assertEqual(parse_checksums(text, ASSET), "c" * 64)

    def test_malformed_lines_are_ignored(self):
        text = f'not a checksum\n\n{NEW_SHA}  {ASSET}\n'
        self.assertEqual(parse_checksums(text, ASSET), NEW_SHA)


class UpdatedFormulaTests(unittest.TestCase):
    def test_updates_url_and_checksum_only(self):
        updated = updated_formula(FORMULA, "0.15.0-build1", ASSET, NEW_SHA)
        self.assertIn(
            f'url "https://github.com/jachy-h/lan-copy/releases/download/v0.15.0-build1/{ASSET}"', updated
        )
        self.assertIn(f'sha256 "{NEW_SHA}"', updated)
        self.assertNotIn(OLD_SHA, updated)
        self.assertEqual(len(FORMULA.splitlines()), len(updated.splitlines()))
        self.assertEqual(
            [line for line in FORMULA.splitlines() if "sha256" not in line and "releases/download" not in line],
            [line for line in updated.splitlines() if "sha256" not in line and "releases/download" not in line],
        )

    def test_indentation_is_preserved(self):
        formula = FORMULA.replace("  url ", "      url ").replace("  sha256 ", "      sha256 ")
        updated = updated_formula(formula, "0.9.0", ASSET, NEW_SHA)
        self.assertIn(f'      sha256 "{NEW_SHA}"', updated)
        self.assertIn(f'      url "https://github.com/jachy-h/lan-copy/releases/download/v0.9.0/{ASSET}"', updated)

    def test_unchanged_returns_input(self):
        formula = updated_formula(FORMULA, "0.15.0", ASSET, NEW_SHA)
        self.assertEqual(updated_formula(formula, "0.15.0", ASSET, NEW_SHA), formula)

    def test_missing_url(self):
        formula = FORMULA.replace("https://github.com/jachy-h/lan-copy/releases/download/", "https://example.invalid/")
        with self.assertRaises(ValueError) as caught:
            updated_formula(formula, "0.15.0", ASSET, NEW_SHA)
        self.assertIn("no Lan Copy release URL", str(caught.exception))

    def test_missing_asset_url(self):
        with self.assertRaises(ValueError) as caught:
            updated_formula(FORMULA, "0.15.0", "Lan-Copy-macOS-Intel.tar.gz", NEW_SHA)
        self.assertIn("no release URL", str(caught.exception))

    def test_duplicate_asset_url(self):
        url = f'  url "https://github.com/jachy-h/lan-copy/releases/download/v0.8.1/{ASSET}"\n'
        formula = FORMULA.replace("  license ", url + "  license ")
        with self.assertRaises(ValueError) as caught:
            updated_formula(formula, "0.15.0", ASSET, NEW_SHA)
        self.assertIn("more than one", str(caught.exception))

    def test_missing_checksum_line(self):
        formula = FORMULA.replace(f'  sha256 "{OLD_SHA}"\n', "")
        with self.assertRaises(ValueError) as caught:
            updated_formula(formula, "0.15.0", ASSET, NEW_SHA)
        self.assertIn("sha256", str(caught.exception))


class UpdateFormulaTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)
        self.formula = self.base / "lancopy.rb"
        self.formula.write_text(FORMULA)
        self.checksums = self.base / "SHA256SUMS"
        self.checksums.write_text(f'{NEW_SHA}  Lan-Copy-Windows-amd64.zip\n{NEW_SHA}  {ASSET}\n')

    def test_update_writes_formula_in_place(self):
        report, changed = update_formula(self.formula, self.checksums, "0.15.0", ASSET)
        self.assertTrue(changed)
        self.assertIn("v0.15.0", report)
        self.assertIn(NEW_SHA, self.formula.read_text())
        self.assertFalse((self.base / ".lancopy.rb.tmp").exists())

    def test_dry_run_does_not_write(self):
        report, changed = update_formula(self.formula, self.checksums, "0.15.0", ASSET, dry_run=True)
        self.assertTrue(changed)
        self.assertIn("dry run", report)
        self.assertEqual(self.formula.read_text(), FORMULA)

    def test_up_to_date_formula_is_not_rewritten(self):
        update_formula(self.formula, self.checksums, "0.15.0", ASSET)
        before = self.formula.stat().st_mtime_ns
        report, changed = update_formula(self.formula, self.checksums, "0.15.0", ASSET)
        self.assertFalse(changed)
        self.assertIn("already points at v0.15.0", report)
        self.assertEqual(self.formula.stat().st_mtime_ns, before)

    def test_invalid_version_leaves_formula_untouched(self):
        for version in ("0.15", "v0.15.0", "0.15.0-rc1", ""):
            with self.subTest(version=version), self.assertRaises(ValueError):
                update_formula(self.formula, self.checksums, version, ASSET)
            self.assertEqual(self.formula.read_text(), FORMULA)

    def test_checksum_failure_leaves_formula_untouched(self):
        self.checksums.write_text(f'{NEW_SHA}  other.tar.gz\n')
        with self.assertRaises(ValueError):
            update_formula(self.formula, self.checksums, "0.15.0", ASSET)
        self.assertEqual(self.formula.read_text(), FORMULA)


if __name__ == "__main__":
    unittest.main()
