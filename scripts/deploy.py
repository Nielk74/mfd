#!/usr/bin/env python3
"""One scheduled deployment check. State and release checkouts live outside Git."""
import argparse
import contextlib
import datetime
import fcntl
import hashlib
import json
import logging
import logging.handlers
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import time
import urllib.request

SHA = re.compile(r"^[0-9a-f]{40}$")


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def atomic_json(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def successful_ci(runs, sha, branch):
    matching = [run for run in runs if run.get("head_sha") == sha
                and run.get("head_branch") == branch and run.get("event") == "push"]
    latest = max(matching, key=lambda run: run["id"], default=None)
    return latest if latest and latest["status"] == "completed" and latest["conclusion"] == "success" else None


@contextlib.contextmanager
def exclusive_lock(path):
    with path.open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            yield False
            return
        try:
            yield True
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)


class Deployer:
    def __init__(self, root):
        self.root = root
        self.config = json.loads((root / "config.json").read_text())
        self.state_file = root / "status.json"
        self.state = json.loads(self.state_file.read_text()) if self.state_file.exists() else {}
        self.env = os.environ.copy()
        self.env.update({"PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
                         "DOCKER_CONTEXT": self.config["docker_context"], "GIT_TERMINAL_PROMPT": "0",
                         "GH_PROMPT_DISABLED": "1"})
        settings_bytes = (root / "settings.env").read_bytes()
        self.settings_sha256 = hashlib.sha256(settings_bytes).hexdigest()
        for line in settings_bytes.decode().splitlines():
            if line.strip() and not line.lstrip().startswith("#"):
                key, value = line.split("=", 1)
                self.env[key.strip()] = value.strip().strip('"').strip("'")
        self.mirror = root / "repository.git"
        self.releases = root / "releases"
        self.releases.mkdir(exist_ok=True)
        (root / "backups").mkdir(exist_ok=True)

    def save(self, **values):
        self.state.update(values, checked_at=now())
        atomic_json(self.state_file, self.state)

    def command(self, args, timeout=60, env=None, output=None):
        result = subprocess.run(args, env=env or self.env, timeout=timeout,
                                stdout=output or subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode:
            # Credentials are never command arguments or included in this error.
            raise RuntimeError(f"{args[0]} {args[1] if len(args) > 1 else ''} exited {result.returncode}: "
                               + result.stderr.decode(errors="replace")[-2000:])
        return result.stdout or b""

    def runtime(self):
        try:
            self.command(["docker", "info", "--format", "{{.ServerVersion}}"], timeout=10)
        except (RuntimeError, subprocess.TimeoutExpired):
            logging.info("Starting Colima")
            self.command(["colima", "start", "--profile", self.config.get("colima_profile", "default")], timeout=240)

    def fetch(self):
        repository = self.config["repository"]
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ValueError("invalid repository")
        if not self.mirror.exists():
            self.command(["git", "init", "--bare", str(self.mirror)])
        base = ["git", "--git-dir", str(self.mirror)]
        self.command([*base, "fetch", "--depth=1", "https://github.com/" + repository + ".git",
                      "refs/heads/" + self.config["branch"]], timeout=120)
        sha = self.command([*base, "rev-parse", "FETCH_HEAD"]).decode().strip()
        if not SHA.fullmatch(sha):
            raise ValueError("invalid commit")
        return sha

    def ci(self, sha):
        endpoint = (f"repos/{self.config['repository']}/actions/workflows/ci.yml/runs"
                    f"?branch={self.config['branch']}&head_sha={sha}&event=push&per_page=20")
        data = json.loads(self.command(["gh", "api", endpoint]))
        return successful_ci(data["workflow_runs"], sha, self.config["branch"])

    def release(self, sha):
        destination = self.releases / sha
        if destination.exists():
            return destination
        staging = self.releases / (sha + ".staging")
        if staging.exists():
            shutil.rmtree(staging)
        staging.mkdir()
        archive = self.root / "source.tar"
        self.command(["git", "--git-dir", str(self.mirror), "archive", "--format=tar",
                      "--output=" + str(archive), sha])
        with tarfile.open(archive) as source:
            source.extractall(staging, filter="data")
        archive.unlink()
        staging.rename(destination)
        return destination

    def compose(self, sha, *args, output=None):
        release = self.releases / sha
        env = self.env | {"MFD_IMAGE": "mfd-app:" + sha, "MFD_REVISION": sha}
        return self.command(["docker", "compose", "--project-name", self.config["project"],
                             "--env-file", str(self.root / "settings.env"), "--project-directory", str(release),
                             "-f", str(release / "compose.yaml"), "--profile", "monitoring", *args],
                            timeout=900 if "build" in args else 240, env=env, output=output)

    def read(self, port, path):
        with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=5) as response:
            return response.read()

    def verify(self, sha):
        port = self.env.get("MFD_PORT", "8088")
        health = json.loads(self.read(port, "/healthz"))
        if health.get("revision") != sha:
            raise RuntimeError("running revision differs from candidate")
        if not all(json.loads(self.read(port, "/readyz")).values()):
            raise RuntimeError("dependency readiness failed")
        self.read(self.env.get("GRAFANA_PORT", "3088"), "/api/health")
        self.read(self.env.get("PROMETHEUS_PORT", "9098"), "/-/ready")

    def wait_healthy(self, sha):
        deadline = time.monotonic() + 45
        while True:
            try:
                self.verify(sha)
                return
            except Exception:
                if time.monotonic() >= deadline:
                    raise
                time.sleep(2)

    def backup(self, sha):
        # A first install may have no database. An existing database must back up
        # successfully before applying a candidate; never silently skip failures.
        container = self.compose(sha, "ps", "-q", "postgres").decode().strip()
        if not container:
            return
        filename = self.root / "backups" / (time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + ".dump")
        with filename.open("wb") as output:
            filename.chmod(0o600)
            self.compose(sha, "exec", "-T", "postgres", "pg_dump", "-U", "mfd", "-d", "mfd", "-Fc", output=output)
        logging.info("Database backup saved: %s", filename.name)

    def deploy(self, sha, ci_run):
        previous = self.state.get("current_sha")
        self.release(sha)
        self.save(phase="building", candidate_sha=sha, ci_url=ci_run["html_url"])
        logging.info("Building %s", sha)
        with (self.root / "build.log").open("wb") as output:
            self.compose(sha, "build", "app", output=output)
        self.backup(sha)
        self.save(phase="deploying")
        try:
            self.compose(sha, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120")
            self.wait_healthy(sha)
        except Exception:
            if previous:
                logging.warning("Candidate failed; restoring %s", previous)
                self.compose(previous, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120")
                self.wait_healthy(previous)
                self.save(phase="rolled_back", failed_sha=sha, retry_after=time.time() + 600)
            raise
        self.save(phase="running", current_sha=sha, previous_sha=previous, deployed_at=now(),
                  settings_sha256=self.settings_sha256,
                  failed_sha=None, retry_after=0, error=None)
        logging.info("Deployed and verified %s", sha)
        # The next scheduled invocation uses the updater from the accepted commit.
        source = self.releases / sha / "scripts" / "deploy.py"
        if source.exists():
            temporary = self.root / "deploy.next.py"
            shutil.copyfile(source, temporary)
            temporary.chmod(0o700)
            temporary.replace(self.root / "deploy.py")

    def tick(self):
        if (self.root / "paused").exists():
            self.save(phase="paused")
            return
        self.runtime()
        current = self.state.get("current_sha")
        if current:
            try:
                self.verify(current)
            except Exception:
                logging.info("Restoring services for %s", current)
                self.compose(current, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120")
                self.wait_healthy(current)
            if self.state.get("settings_sha256") != self.settings_sha256:
                logging.info("Applying updated private deployment settings")
                self.compose(current, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120")
                self.wait_healthy(current)
                self.save(settings_sha256=self.settings_sha256)
        sha = self.fetch()
        self.save(observed_sha=sha)
        if sha == current:
            self.save(phase="running", error=None)
            return
        ci_run = self.ci(sha)
        if not ci_run:
            if self.state.get("phase") != "waiting_for_ci" or self.state.get("candidate_sha") != sha:
                logging.info("Waiting for successful CI on %s", sha)
            self.save(phase="waiting_for_ci", candidate_sha=sha)
            return
        if self.state.get("failed_sha") == sha and time.time() < self.state.get("retry_after", 0):
            self.save(phase="retry_backoff")
            return
        try:
            self.deploy(sha, ci_run)
        except Exception:
            self.save(failed_sha=sha, retry_after=time.time() + 600)
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", type=Path, default=Path.home() / ".local/share/mfd")
    parser.add_argument("--status", action="store_true")
    args = parser.parse_args()
    if args.status:
        print((args.state_dir / "status.json").read_text())
        return 0
    os.umask(0o077)
    handler = logging.handlers.RotatingFileHandler(args.state_dir / "deploy.log", maxBytes=2_000_000, backupCount=3)
    logging.basicConfig(level=logging.INFO, handlers=[handler], format="%(asctime)s %(levelname)s %(message)s")
    with exclusive_lock(args.state_dir / "deploy.lock") as acquired:
        if not acquired:
            return 0
        deployer = None
        try:
            deployer = Deployer(args.state_dir)
            deployer.tick()
        except Exception as error:
            logging.exception("Deployment check failed")
            if deployer:
                deployer.save(phase="error", error=str(error))
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
