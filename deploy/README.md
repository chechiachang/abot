# systemd

## Ubuntu dependencies

```sh
sudo apt-get update
sudo apt-get install -y bash ca-certificates coreutils curl ffmpeg git gawk jq pandoc poppler-utils tar
```

## Install an instance

Download the service, updater, example config, and system instructions. Replace `main` with a release tag to pin these files.

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

Edit `/etc/abot/main/config.yaml`: set the Azure base URL and model, change `coding.workdir` to `/srv/abot/main` (or another directory writable by `abot`), and add your numeric Telegram user ID to `telegram.allowed_user_ids`. The example list is empty, which denies all users.

Add `TELEGRAM_BOT_TOKEN` and `AZURE_OPENAI_API_KEY` to `/etc/abot/main.env`:

```sh
sudoedit /etc/abot/main/config.yaml
sudoedit /etc/abot/main.env
```

systemd reads the environment file and passes its values to abot; the file is root-owned and mode `0600`. The config and system-instruction files are readable by the `abot` service user.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now abot@main
sudo systemctl status --no-pager abot@main
```

Each start downloads and checksum-verifies the latest Linux release for the host architecture before running `abot telegram serve`. For another bot, create its own `/etc/abot/<name>/config.yaml`, `SYSTEM.md`, and `/etc/abot/<name>.env`, then enable `abot@<name>`; its runtime state is stored separately.
