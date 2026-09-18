"""Regression tests use synthetic values; no real deployment data is needed."""
from pathlib import Path
import os
import subprocess
import sys
import tempfile
import unittest

from check_sensitive import scan


SCANNER = Path(__file__).with_name("check_sensitive.py").resolve()
INSTALLER = Path(__file__).with_name("install_hooks.py").resolve()
HOOK = SCANNER.parents[2] / ".githooks/pre-commit"


class RulesTest(unittest.TestCase):
    def rules(self, value, path="example.txt"):
        return {rule for _, rule in scan(path, value.encode())}

    def test_addresses(self):
        # Build globally routed fixtures at runtime so the fixtures themselves remain safe.
        public = ".".join(["8"] * 4)
        v6 = ":".join(["2001", "4860", "4860", "", "8888"])
        for address in (public, v6, "::ffff:" + public):
            self.assertIn("public-ip", self.rules(address))
        for address in ("127.0.0.1", "0.0.0.0", "10.0.0.1", "192.0.2.1", "198.51.100.2",
                        "203.0.113.3", "::1", "2001:db8::1", "fe80::1", "999.999.999.999"):
            self.assertNotIn("public-ip", self.rules(address))

    def test_credentials(self):
        host = ".".join(["db", "company", "tld"])
        cases = [
            ("sk-" + "Ab12" * 10, "provider-token"),
            ("ghp_" + "a" * 36, "provider-token"),
            ("AKIA" + "A" * 16, "provider-token"),
            ("-----BEGIN " + "OPENSSH PRIVATE KEY-----", "private-key"),
            ('API_KEY="' + "Ab123cd4" * 4 + '"', "credential-literal"),
            ('{"password": "' + "Ab123cd4" * 4 + '"}', "credential-literal"),
            ("Bearer " + "Ab123cd4" * 4, "credential-literal"),
            ("postgres://user:password@" + host + "/db", "url-credentials"),
            ("eyJ" + "a" * 20 + "." + "b" * 20 + "." + "c" * 20, "jwt"),
        ]
        for value, rule in cases:
            with self.subTest(rule=rule):
                self.assertIn(rule, self.rules(value))

    def test_deployment_hosts(self):
        host = ".".join(["box", "company", "tld"])
        for value in ('VPS_HOST=' + host, 'VPS_USER=' + 'operator',
                      "ssh operator@" + host, "WAVE_QA_SSH=" + "operator@" + host):
            self.assertTrue(self.rules(value))

    def test_examples_and_env_references(self):
        for value in ('API_KEY="${WAVE_MODEL_API_KEY}"', 'VPS_HOST="${VPS_HOST}"',
                      "ssh ${VPS_USER}@${VPS_HOST}", "ssh user@host.example.com",
                      'TOKEN="<your-long-placeholder>"', "PASSWORD=replace-me",
                      "postgres://wave:wave-test@127.0.0.1:5432/test", "https://api.deepseek.com",
                      "127.0.0.1::5432", "192.88.99.1", '"https://user:password@example.com"',
                      'tokens = *final.Usage.PromptTokens + *final.Usage.CompletionTokens'):
            with self.subTest(value=value):
                self.assertFalse(self.rules(value))

    def test_file_names_and_binary_keys(self):
        for path in (".env", "deploy/.env.production", "id_ed25519", "server.key", ".aws/credentials"):
            self.assertIn("sensitive-file", self.rules("", path))
        self.assertFalse(self.rules("", "deploy/.env.example"))
        self.assertIn("provider-token", {r for _, r in scan("image.bin", b"\0sk-" + b"Ab12" * 10)})


class GitTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = {**os.environ, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull}
        self.git("init", "-q")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.com")
        self.git("config", "commit.gpgsign", "false")

    def git(self, *args, check=True):
        return subprocess.run(["git", *args], cwd=self.root, env=self.env, check=check, capture_output=True, text=True)

    def run_check(self, *args):
        return subprocess.run([sys.executable, str(SCANNER), *args], cwd=self.root,
                              env=self.env, capture_output=True, text=True)

    def write(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)

    def test_staged_content_and_redacted_output(self):
        value = "sk-" + "Ab12" * 10
        self.write("space name.txt", value)
        self.git("add", ".")
        self.write("space name.txt", "fixed only in working tree")
        result = self.run_check()
        self.assertEqual(result.returncode, 1)
        self.assertIn("space name.txt", result.stderr)
        self.assertNotIn(value, result.stderr + result.stdout)
        self.git("add", ".")
        self.write("space name.txt", value)
        self.assertEqual(self.run_check().returncode, 0)

    def test_deleted_files_and_all_mode(self):
        self.write("file.txt", "sk-" + "Ab12" * 10)
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        self.assertEqual(self.run_check().returncode, 0)
        self.assertEqual(self.run_check("--all").returncode, 1)
        self.git("rm", "file.txt")
        self.assertEqual(self.run_check().returncode, 0)

    def test_intermediate_commit_is_checked(self):
        self.write("file.txt", "safe")
        self.git("add", ".")
        self.git("commit", "-qm", "base")
        base = self.git("rev-parse", "HEAD").stdout.strip()
        self.write("file.txt", "sk-" + "Ab12" * 10)
        self.git("add", ".")
        self.git("commit", "-qm", "synthetic leak")
        self.git("rm", "file.txt")
        self.git("commit", "-qm", "remove")
        self.assertEqual(self.run_check("--range", base + "..HEAD").returncode, 1)

    def test_actual_hook_blocks_commit_and_installer_preserves_hooks(self):
        self.write("tools/git/check_sensitive.py", SCANNER.read_text())
        self.write(".githooks/pre-commit", HOOK.read_text())
        old_hook = self.root / ".git/hooks/pre-commit"
        old_hook.write_text("#!/bin/sh\nexit 0\n")
        install = lambda: subprocess.run([sys.executable, str(INSTALLER)], cwd=self.root,
                                         env=self.env, capture_output=True, text=True)
        self.assertEqual(install().returncode, 1)
        self.assertTrue(old_hook.exists())
        old_hook.unlink()
        self.assertEqual(install().returncode, 0)
        self.assertEqual(install().returncode, 0)
        self.write("file.txt", "sk-" + "Ab12" * 10)
        self.git("add", "file.txt")
        self.assertNotEqual(self.git("commit", "-qm", "blocked", check=False).returncode, 0)
        self.write("file.txt", "safe")
        self.git("add", "file.txt")
        self.assertEqual(self.git("commit", "-qm", "allowed").returncode, 0)


if __name__ == "__main__":
    unittest.main()
