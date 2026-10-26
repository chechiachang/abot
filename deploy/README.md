# systemd deployment

Install abot as a dedicated systemd service on Ubuntu. The service downloads and checksum-verifies the release binary on startup.

## 1. Install host dependencies

```sh
sudo apt-get update
sudo apt-get install -y bash ca-certificates coreutils curl ffmpeg git gawk jq pandoc poppler-utils tar
```

## 2. Install the service files

Choose `deploy_ref` as a release tag to pin deployment files, or use `main` to track the latest versions.

```sh
set -eu
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
deploy_ref=main
raw_url="https://raw.githubusercontent.com/chechiachang/abot/$deploy_ref"
curl -fsSL "$raw_url/deploy/abot@.service" -o "$tmpdir/abot@.service"
curl -fsSL "$raw_url/deploy/update-release.sh" -o "$tmpdir/update-release.sh"
curl -fsSL "$raw_url/config.example.yaml" -o "$tmpdir/config.yaml"
curl -fsSL "$raw_url/SYSTEM.md" -o "$tmpdir/SYSTEM.md"

if ! id -u abot >/dev/null 2>&1; then
  sudo useradd --system --create-home --home-dir /var/lib/abot --shell /usr/sbin/nologin abot
fi
sudo install -D -m 0644 "$tmpdir/abot@.service" /etc/systemd/system/abot@.service
sudo install -D -m 0755 "$tmpdir/update-release.sh" /usr/local/libexec/abot-update-release
sudo install -d -o root -g abot -m 0750 /etc/abot/main
if [ ! -e /etc/abot/main/config.yaml ]; then
  sudo install -o root -g abot -m 0640 "$tmpdir/config.yaml" /etc/abot/main/config.yaml
fi
if [ ! -e /etc/abot/main/SYSTEM.md ]; then
  sudo install -o root -g abot -m 0640 "$tmpdir/SYSTEM.md" /etc/abot/main/SYSTEM.md
fi
if [ ! -e /etc/abot/main.env ]; then
  sudo install -o root -g root -m 0600 /dev/null /etc/abot/main.env
fi
sudo install -d -o abot -g abot -m 0750 /srv/abot/main
```

## 3. Configure and start

Edit `/etc/abot/main/config.yaml`: set the Azure base URL and model, set `coding.workdir` to `/srv/abot/main` (or another directory writable by `abot`), and add your numeric Telegram user ID to `telegram.allowed_user_ids`. The example list is empty and denies all users.

Add `TELEGRAM_BOT_TOKEN` and `AZURE_OPENAI_API_KEY` to `/etc/abot/main.env`; keep this root-owned file restricted to mode `0600`:

```sh
sudoedit /etc/abot/main/config.yaml
sudoedit /etc/abot/main.env
sudo systemctl daemon-reload
sudo systemctl enable --now abot@main
sudo systemctl status --no-pager abot@main
```

Each service start fetches and checksum-verifies the latest Linux release for the host architecture, then runs `abot telegram serve`. To run another bot, provide separate `/etc/abot/<name>/config.yaml`, `SYSTEM.md`, and `/etc/abot/<name>.env`, then enable `abot@<name>`; runtime state is isolated per instance.
