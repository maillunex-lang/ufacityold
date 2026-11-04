"""Проверки CI-доставки: локальные подмены команд, без сети, VPS и секретов."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / ".github/scripts/deploy-server.sh"
SHA = "a" * 40


class DeployServerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "build").mkdir()
        (self.root / "build/lunexgame").write_bytes(b"test binary")
        (self.root / "build/lunexgame.sha256").write_text("b" * 64 + "\n")
        bindir = self.root / "bin"
        bindir.mkdir()
        self.env = dict(os.environ, PATH=str(bindir) + ":" + os.environ["PATH"],
                        GITHUB_SHA=SHA, HOST="example.invalid", USER_="test-deploy",
                        PUBLIC_URL="https://example.invalid/LunexGame",
                        CALLS=str(self.root / "calls"), MODE="success",
                        INPUT_COPY=str(self.root / "input-copy"))
        self.command("ssh", '''
echo "ssh $*" >> "$CALLS"
case "$MODE" in
  ssh_hang) exec /bin/sleep 30;;
  ssh_error) exit 255;;
esac
cat > "$INPUT_COPY"
echo "OK"
''')
        self.command("curl", '''
echo "curl $*" >> "$CALLS"
if [[ "$MODE" == http_error ]]; then exit 28; fi
case "${!#}" in
  */version)
    if [[ "$MODE" == old_commit ]]; then
      echo '{"commit":"old"}'
    else
      echo "{\\"commit\\":\\"$GITHUB_SHA\\"}"
    fi;;
  */health)
    if [[ "$MODE" == bad_health ]]; then exit 22; fi
    echo '{"status":"ok"}';;
esac
''')
        # Реально завершаем зависший SSH, сокращая только длительности для теста.
        self.command("timeout", '''
echo "timeout $*" >> "$CALLS"
[[ "$1" == --signal=TERM && "$2" == --kill-after=15s && "$3" == 300s ]] || exit 99
shift 3
limit=5s
if [[ "$MODE" == ssh_hang ]]; then limit=0.2s; fi
exec /usr/bin/timeout --signal=TERM --kill-after=1s "$limit" "$@"
''')
        self.command("sleep", "exit 0\n")

    def command(self, name, source):
        path = self.root / "bin" / name
        path.write_text("#!/usr/bin/env bash\nset -euo pipefail\n" + source)
        path.chmod(0o755)

    def run_deploy(self, mode="success"):
        self.env["MODE"] = mode
        result = subprocess.run(["bash", str(SCRIPT)], cwd=self.root, env=self.env,
                                capture_output=True, text=True, timeout=5)
        calls = self.root / "calls"
        return result, calls.read_text() if calls.exists() else ""

    def test_success_transfers_binary_and_checks_exact_commit_and_health(self):
        result, calls = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Этап 1/2", result.stdout)
        self.assertIn("Этап 2/2", result.stdout)
        self.assertIn("подтверждены", result.stdout)
        self.assertIn("deploy " + SHA + " " + "b" * 64, calls)
        self.assertEqual((self.root / "input-copy").read_bytes(), b"test binary")
        for option in ("StrictHostKeyChecking=yes", "BatchMode=yes", "ConnectTimeout=20",
                       "ConnectionAttempts=1", "ServerAliveInterval=15", "ServerAliveCountMax=3"):
            self.assertIn(option, calls)
        curl_calls = [line for line in calls.splitlines() if line.startswith("curl ")]
        self.assertEqual(len(curl_calls), 2)
        for line in curl_calls:
            self.assertIn("--connect-timeout 5 --max-time 10", line)
        self.assertTrue(curl_calls[0].endswith("/api/version"))
        self.assertTrue(curl_calls[1].endswith("/api/health"))

    def test_hanging_ssh_is_terminated_without_retry_or_http_success(self):
        result, calls = self.run_deploy("ssh_hang")
        self.assertEqual(result.returncode, 124, result.stderr)
        self.assertIn("превысила лимит времени", result.stderr)
        self.assertEqual(sum(line.startswith("ssh ") for line in calls.splitlines()), 1)
        self.assertNotIn("curl ", calls)
        self.assertNotIn("Этап 2/2", result.stdout)

    def test_ssh_error_is_preserved_and_http_not_called(self):
        result, calls = self.run_deploy("ssh_error")
        self.assertEqual(result.returncode, 255)
        self.assertNotIn("curl ", calls)

    def test_old_commit_http_failure_and_bad_health_fail_after_ten_attempts(self):
        for mode in ("old_commit", "http_error", "bad_health"):
            with self.subTest(mode=mode):
                (self.root / "calls").unlink(missing_ok=True)
                result, calls = self.run_deploy(mode)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertEqual(calls.count("/api/version"), 10)
                self.assertIn("не подтвердили релиз", result.stderr)
                self.assertNotIn("подтверждены для", result.stdout)

    def test_invalid_sha_stops_before_network(self):
        self.env["GITHUB_SHA"] = "invalid"
        result, calls = self.run_deploy()
        self.assertEqual(result.returncode, 2)
        self.assertEqual(calls, "")


if __name__ == "__main__":
    unittest.main()
