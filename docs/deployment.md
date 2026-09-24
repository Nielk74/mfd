# This Mac's deployment

The complete stack runs under the Compose project `mfd`. All published ports bind to `0.0.0.0`; the monitoring profile is enabled by the updater.

| Service | Host port | HTTPS hostname |
| --- | --- | --- |
| Lab / API | 8088 | mfd.aklein.fr |
| Grafana | 3088 | mfd-grafana.aklein.fr |
| Prometheus | 9098 | mfd-prometheus.aklein.fr |
| PostgreSQL | 5432 | TCP access through the host address |
| Redis | 6379 | TCP access through the host address |
| NATS clients | 4222 | TCP access through the host address |
| NATS monitoring | 8222 | HTTP access through the host address |

Caddy terminates HTTPS and proxies the three web hostnames to the Mac's LAN address. Caddy configuration is managed separately from the application release. Its administrative credentials are never stored in this repository. Database and queue protocols are not HTTP virtual hosts.

## Automatic updates

`scripts/install_deploy.py` installs a cron entry at reboot and every minute. It uses the existing user scheduler on this Mac. No self-hosted Actions runner or incoming webhook is needed. It preserves unrelated scheduled jobs and saves the previous crontab.

```sh
python3 scripts/install_deploy.py
```

The updater reads `Nielk74/mfd`'s `main` branch. A candidate must have a successful **push** run of `ci.yml` for that exact SHA; pending, failed and PR-only checks do not deploy. The latest main commit wins if several commits arrive during one build. A commit that arrives during a deployment is picked up on the following check.

For each accepted candidate it:

1. Fetches source into a separate bare repository and extracts a commit-specific checkout.
2. Builds an image tagged with the full SHA before replacing the running app.
3. Saves a PostgreSQL custom-format backup if a database is already running.
4. Runs Compose with the existing project and named volumes, including monitoring.
5. Verifies the compiled revision via `/healthz`, dependency readiness, Grafana and Prometheus.
6. Records the deployed SHA. On health failure, attempts to restore the previous accepted checkout and image. Failed candidates retry after ten minutes; newer commits can proceed sooner.

Overlapping checks take a nonblocking file lock and exit. Docker services restart automatically; if Docker is unavailable, the scheduled check attempts to start the existing Colima profile. This configures reboot recovery without rebooting the Mac during installation. Sleep, loss of power and unavailable networking delay updates.

## Files and operations

The default state directory is `~/.local/share/mfd`, outside the working repository:

- `config.json`: repository, branch, project and Docker context.
- `settings.env`: network bindings, ports and worker count.
- `status.json`: current/candidate SHA, CI link, last check, deployment time and error.
- `deploy.log`: rotating update log; `build.log`: latest build output.
- `releases/<sha>/`: immutable release checkout; the development checkout is untouched.
- `backups/*.dump`: database backup before each deployment.

```sh
python3 ~/.local/share/mfd/deploy.py --status
tail -f ~/.local/share/mfd/deploy.log
touch ~/.local/share/mfd/paused       # pause future checks; services keep running
rm ~/.local/share/mfd/paused          # resume on the next minute
```

Run the installed updater manually for an immediate check. The same lock protects manual and scheduled invocations. Reinstall after changing installer behavior; the updater script itself follows accepted deployments.

Release checkouts, images and backups are retained for recovery. Prune them deliberately after reviewing `current_sha` and `previous_sha`; do not run volume-deleting cleanup against this project. Deployment rollback restores containers, not a database schema downgrade. Future migrations must remain compatible with the prior release or define an explicit recovery procedure. Backups are not automatically restored over newer data.

This is the requested network-accessible fixture lab. Application authentication is still a roadmap item, Grafana allows anonymous viewing, and infrastructure uses development credentials. Network access is not a completed live-trading security deployment.
