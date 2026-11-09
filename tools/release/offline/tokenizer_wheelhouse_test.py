import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import tokenizer_wheelhouse as fixture


class TokenizerWheelhouseTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.output = self.root / "output"
        self.runtime = {"linux_manufacturing": {
            "platforms": ["manylinux_2_28_x86_64"], "abis": ["cp312", "abi3", "none"],
            "image_installer": {"version": "1.0", "filename": "pip-1.0-py3-none-any.whl"},
        }}
        pip = self.write_wheel("pip-1.0-py3-none-any.whl", "pip")
        self.runtime["linux_manufacturing"]["image_installer"].update(
            sha256=self.digest(pip), size_bytes=pip.stat().st_size,
        )

    @staticmethod
    def digest(path):
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def write_wheel(self, filename, distribution, *, notice=True):
        path = self.source / filename
        prefix = distribution + "-1.0.dist-info/"
        with zipfile.ZipFile(path, "w") as wheel:
            wheel.writestr(prefix + "METADATA", f"Name: {distribution}\nVersion: 1.0\nLicense-Expression: MIT\n")
            if notice:
                wheel.writestr(prefix + "licenses/LICENSE", "Synthetic test notice")
        return path

    def test_projection_reproduces_the_verified_lock(self):
        training = Path(fixture.__file__).resolve().parents[3] / "runtimes" / "training"
        raw = (training / "requirements" / fixture.LOCK).read_bytes()
        runtime = json.loads((training / "runtime.json").read_bytes())
        expected = runtime["locks"][fixture.LOCK]
        pins, projected = fixture.project_lock(raw, expected)
        self.assertEqual(pins.keys(), fixture.DISTRIBUTIONS)
        if expected == "250fee15d9e7eb9ec71793c743b02f62870094bb494a57a0c463c5ce29399946":
            self.assertEqual(hashlib.sha256(projected).hexdigest(),
                             "7873b0c15e07995060826373087b050437d10cee5af935e6c717b9e10622c19d")
        with self.assertRaisesRegex(ValueError, "Training lock SHA-256 mismatch"):
            fixture.project_lock(raw + b"\n", expected)

    def test_projection_rejects_missing_duplicate_and_direct_url(self):
        pin = b"sample==1.0 --hash=sha256:" + b"f" * 64 + b"\n"
        with patch.object(fixture, "DISTRIBUTIONS", {"sample"}):
            for invalid, error in ((b"", "Missing tokenizer pins"), (pin * 2, "Duplicate tokenizer lock pin"),
                                   (b"sample @ https://invalid.example/wheel\n", "Invalid training lock entry")):
                with self.subTest(error=error), self.assertRaisesRegex(ValueError, error):
                    fixture.project_lock(invalid, hashlib.sha256(invalid).hexdigest())

    def test_admission_copies_compatible_pins_and_inventories_notices(self):
        wheel = self.write_wheel("sample-1.0-cp310-abi3-manylinux_2_28_x86_64.whl", "sample")
        result = fixture.wheelhouse(self.source, self.output, self.runtime, {"sample": ("1.0", {self.digest(wheel)})})
        self.assertEqual(result.keys(), {"sample", "pip"})
        self.assertEqual(result["sample"]["license_expression"], "MIT")
        self.assertEqual(result["sample"]["notices"], {
            "sample-1.0.dist-info/licenses/LICENSE": hashlib.sha256(b"Synthetic test notice").hexdigest(),
        })
        wheel.write_bytes(b"changed original")
        self.assertEqual(self.digest(self.output / wheel.name), result["sample"]["sha256"])

    def test_admission_refuses_missing_bootstrap_and_missing_notice(self):
        (self.source / "pip-1.0-py3-none-any.whl").unlink()
        wheel = self.write_wheel("sample-1.0-py3-none-any.whl", "sample", notice=False)
        with self.assertRaisesRegex(ValueError, r"missing wheels: pip; missing notices: sample"):
            fixture.wheelhouse(self.source, self.output, self.runtime, {"sample": ("1.0", {self.digest(wheel)})})

    def test_admission_refuses_an_incompatible_native_wheel(self):
        wheel = self.write_wheel("sample-1.0-cp312-cp312-manylinux_2_28_aarch64.whl", "sample")
        with self.assertRaisesRegex(ValueError, "Incompatible Linux amd64 wheel"):
            fixture.wheelhouse(self.source, self.output, self.runtime, {"sample": ("1.0", {self.digest(wheel)})})

    def test_admission_refuses_ambiguous_or_altered_pins(self):
        wheel = self.write_wheel("sample-1.0-py3-none-any.whl", "sample")
        extra = self.write_wheel("sample-1.0-py2.py3-none-any.whl", "sample")
        pin = {"sample": ("1.0", {self.digest(wheel), self.digest(extra)})}
        with self.assertRaisesRegex(ValueError, "duplicate wheel pin"):
            fixture.wheelhouse(self.source, self.output, self.runtime, pin)
        extra.unlink()
        wheel.write_bytes(b"changed source")
        with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
            fixture.wheelhouse(self.source, self.root / "retry", self.runtime, pin)

    def test_contract_refuses_group_writable_source(self):
        contract = self.root / "runtime.json"
        contract.write_bytes(b"{}")
        contract.chmod(0o664)
        with self.assertRaisesRegex(ValueError, "Unprotected contract"):
            fixture.read_contract(contract)
        contract.chmod(0o644)
        self.assertEqual(fixture.read_contract(contract), b"{}")

    def test_model_file_rejects_a_symlink_or_incorrect_size(self):
        regular = self.root / "config.json"
        regular.write_bytes(b"catalogued fixture")
        link = self.root / "linked.json"
        link.symlink_to(regular)
        with self.assertRaises(OSError):
            fixture.copy_checked(link, self.output, {self.digest(regular)})
        with self.assertRaisesRegex(ValueError, "Size mismatch"):
            fixture.copy_checked(regular, self.output, {self.digest(regular)}, 2)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
