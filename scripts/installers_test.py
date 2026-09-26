import hashlib
import io
import os
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class InstallerTests(unittest.TestCase):
    def test_install_and_reject_corrupt_download(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            system = {"Linux": "linux", "Darwin": "darwin"}[platform.system()]
            arch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
            asset = directory / f"brokk-town-v0.1.0-town-{system}-{arch}.tar.gz"
            payload = b"#!/bin/sh\nprintf 'fixture bt\\n'\n"
            with tarfile.open(asset, "w:gz") as archive:
                files = {"bt": payload, "README.md": b"Town", "BUILD.json": b"{}",
                         "LICENSE": b"license", "NOTICE": b"notice", "licenses/THIRD_PARTY_NOTICES.txt": b"notices"}
                for name, data in files.items():
                    info = tarfile.TarInfo(name)
                    info.mode, info.size = 0o755, len(data)
                    archive.addfile(info, io.BytesIO(data))
            (directory / "checksums.txt").write_text(f"{hashlib.sha256(asset.read_bytes()).hexdigest()}  {asset.name}\n")
            tools = directory / "tools"
            tools.mkdir()
            curl = tools / "curl"
            curl.write_text(f"#!{sys.executable}\nimport os, pathlib, shutil, sys\nargs=sys.argv[1:]\nshutil.copyfile(pathlib.Path(os.environ['FIXTURE_ASSETS']) / args[-1].rsplit('/',1)[-1], args[args.index('--output')+1])\n")
            curl.chmod(0o755)
            destination = directory / "installed"
            env = dict(os.environ, PATH=f"{tools}:{os.environ['PATH']}", FIXTURE_ASSETS=str(directory), INSTALL_DIR=str(destination))
            command = ["sh", str(ROOT / "install.sh"), "v0.1.0"]
            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((destination / "bt").read_bytes(), payload)
            self.assertTrue((destination / "bt").is_symlink())
            installed = (destination / "bt").resolve().parent
            self.assertEqual({p.relative_to(installed).as_posix() for p in installed.rglob("*") if p.is_file()}, set(files))
            asset.write_bytes(b"damaged")
            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("checksum mismatch", result.stderr)
            self.assertEqual((destination / "bt").read_bytes(), payload)


if __name__ == "__main__":
    unittest.main()
