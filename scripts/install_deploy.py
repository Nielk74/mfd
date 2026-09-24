#!/usr/bin/env python3
"""Install the macOS cron updater without changing unrelated scheduled jobs."""
import argparse
import datetime
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys

BEGIN = "# BEGIN MFD_DEPLOY (managed)"
END = "# END MFD_DEPLOY"


def cron_text(existing, command):
    if BEGIN in existing:
        start = existing.index(BEGIN)
        end = existing.index(END, start) + len(END)
        existing = existing[:start] + existing[end:]
    block = f"{BEGIN}\n@reboot {command}\n* * * * * {command}\n{END}\n"
    return existing.rstrip() + "\n" + block


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", type=Path, default=Path.home() / ".local/share/mfd")
    args = parser.parse_args()
    root = args.state_dir.expanduser().resolve()
    source = Path(__file__).resolve().parent.parent
    os.umask(0o077)
    root.mkdir(parents=True, exist_ok=True)
    root.chmod(0o700)
    updater = root / "deploy.py"
    temporary = root / "deploy.install.py"
    shutil.copyfile(source / "scripts/deploy.py", temporary)
    temporary.chmod(0o700)
    temporary.replace(updater)
    config = root / "config.json"
    if not config.exists():
        config.write_text(json.dumps({"repository": "Nielk74/mfd", "branch": "main",
                                      "project": "mfd", "docker_context": "colima",
                                      "colima_profile": "default"}, indent=2) + "\n")
    if not (root / "settings.env").exists():
        shutil.copyfile(source / ".env.example", root / "settings.env")
    result = subprocess.run(["crontab", "-l"], capture_output=True, text=True)
    if result.returncode and "no crontab" not in result.stderr.lower():
        raise RuntimeError("Could not read existing crontab: " + result.stderr)
    existing = result.stdout
    stamp = datetime.datetime.now().strftime("%Y%m%dT%H%M%S")
    (root / f"crontab.before-{stamp}").write_text(existing)
    command = shlex.join([sys.executable, str(updater), "--state-dir", str(root)])
    if "%" in command or "\n" in command:
        raise ValueError("cron paths cannot contain percent signs or newlines")
    subprocess.run(["crontab", "-"], input=cron_text(existing, command), text=True, check=True)
    print(f"Installed: {updater}\nSchedule: every minute and at reboot\nState: {root / 'status.json'}")


if __name__ == "__main__":
    main()
