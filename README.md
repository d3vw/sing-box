# sing-box

The universal proxy platform.

## Quick Start

This fork is intended to stay close to upstream while adding custom features.

Recommended install flow:

1. Install the official sing-box release first:

   ```bash
   curl -fsSL https://sing-box.app/install.sh | sh -s -- --beta
   ```

2. Replace the upstream `sing-box` binary with the custom binary from this fork.

That keeps the setup simple and makes upgrades easier: you keep the same config and only swap the executable.

If you want to install the custom binary from the latest release, use:

```bash
TAG=$(curl -fsSL https://api.github.com/repos/d3vw/sing-box/releases/latest | jq -r .tag_name)
curl -L -o sing-box-linux-amd64.tar.gz "https://github.com/d3vw/sing-box/releases/download/${TAG}/sing-box-linux-amd64.tar.gz"
tar -xzf sing-box-linux-amd64.tar.gz
sudo install -m 755 sing-box-linux-amd64 /usr/local/bin/sing-box
```

If you already know the tag, replace `${TAG}` with the version you want.

## Downloads

- Custom Linux amd64 builds are published from the `custom` branch.
- Release artifacts are attached to GitHub Releases.

## Documentation

- Upstream docs: https://sing-box.sagernet.org
- For this fork, read the release notes and workflow outputs in the repository.
