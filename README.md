<div align="center">

# 🖥️ terraform-provider-hetznerrobot

**Manage Hetzner Robot as code — dedicated servers, IPs, failover, rDNS, firewalls, vSwitches, Storage Boxes and orders, reconciled by OpenTofu**

[![Release](https://img.shields.io/github/v/release/kirchDev/terraform-provider-hetznerrobot?style=flat-square&label=release&color=1E88E5)](https://github.com/kirchDev/terraform-provider-hetznerrobot/releases/latest)
[![OpenTofu Registry](https://img.shields.io/badge/opentofu-kirchdev%2Fhetznerrobot-FFDA18?style=flat-square&logo=opentofu&logoColor=black)](https://search.opentofu.org/provider/kirchdev/hetznerrobot/latest)
[![Terraform Registry](https://img.shields.io/badge/terraform-kirchdev%2Fhetznerrobot-7b42bc?style=flat-square&logo=terraform&logoColor=white)](https://registry.terraform.io/providers/kirchDev/hetznerrobot/latest)
[![Tests](https://img.shields.io/github/actions/workflow/status/kirchDev/terraform-provider-hetznerrobot/ci.yml?branch=main&style=flat-square&label=tests)](https://github.com/kirchDev/terraform-provider-hetznerrobot/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/github/license/kirchDev/terraform-provider-hetznerrobot?style=flat-square&color=1E88E5)](LICENSE)

</div>

---

```hcl
resource "hetznerrobot_failover" "vip" {
  ip               = "88.99.1.10"
  active_server_ip = data.hetznerrobot_server.standby.server_ip
}
```

Your dedicated servers declared in HCL and reconciled by OpenTofu — not clicked together in Robot. Covers the complete [Robot webservice](https://robot.hetzner.com/doc/webservice/en.html); the Hetzner Cloud side stays with the official [`hcloud`](https://search.opentofu.org/provider/hetznercloud/hcloud/latest) provider.

> [!IMPORTANT]
> **Pre-1.0.** The everyday parts are verified against the live webservice; what could not be tried on the test account — resets, the firewall of a running server, reinstalls, failover, Storage Boxes, orders — is built from the official documentation and covered by mock tests only. See [Test status](#-test-status). Pin an exact version, and try `test = true` before a real order.

## 📦 Install & run

```hcl
terraform {
  required_providers {
    hetznerrobot = {
      source  = "kirchdev/hetznerrobot"
      version = "~> 0.1"
    }
  }
}

provider "hetznerrobot" {} # HETZNER_ROBOT_USER / HETZNER_ROBOT_PASSWORD

data "hetznerrobot_server" "web" {
  server_number = 321
}

resource "hetznerrobot_rdns" "web" {
  ip  = data.hetznerrobot_server.web.server_ip
  ptr = "web1.example.com"
}

resource "hetznerrobot_firewall" "web" {
  server_number = data.hetznerrobot_server.web.server_number
  input_rules = [
    { name = "ssh", ip_version = "ipv4", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "https", ip_version = "ipv4", dst_port = "443", protocol = "tcp", action = "accept" },
  ]
}
```

```bash
export HETZNER_ROBOT_USER="..."      # Robot → Settings → Web service and app settings
export HETZNER_ROBOT_PASSWORD="..."
tofu plan
```

> [!WARNING]
> Robot blocks the calling IP for **ten minutes after three failed logins**. Check the credentials before a plan loops over them.

## ✨ Features

- **🖥️ Complete webservice** — every documented endpoint is a resource or a data source, or deliberately left out (see below).
- **🛡️ Safe with money and machines** — orders save their state the moment Robot accepts them and never re-order on a timeout; destroying an order, a server or a Storage Box only forgets it — cancelling is its own resource.
- **🔁 One-shot actions as resources** — resets and Wake on LAN run on create and again whenever their `triggers` change; boot configurations survive the reboot that consumes them without planning a re-activation.
- **🧭 Import everything** — by the natural key (`server_number`, IP, fingerprint, `<storagebox_id>/<name>`, transaction id).
- **🎯 Only real changes** — Optional + Computed settings keep their live value when left out of the configuration.
- **⏱️ Honours the limits** — empty collections (Robot's 404) read as empty lists, Storage Box writes keep Robot's 5-second spacing, failed logins and non-idempotent writes are never retried.
- **🚀 OpenTofu & Terraform** — published as `kirchdev/hetznerrobot` on both registries.

## 🗺️ Coverage

<details>
<summary>Resources & data sources</summary>

- **Servers** — `hetznerrobot_server` (adopt + rename), `hetznerrobot_server_cancellation`; data sources `hetznerrobot_server(s)`, `hetznerrobot_server_cancellation`.
- **IPs & subnets** — `hetznerrobot_ip`, `hetznerrobot_subnet` (traffic warnings), `hetznerrobot_ip_mac`, `hetznerrobot_subnet_mac`, `hetznerrobot_ip_cancellation`, `hetznerrobot_subnet_cancellation`; data sources `hetznerrobot_ip(s)`, `hetznerrobot_subnet(s)`, `hetznerrobot_subnet_mac`, `…_ip_cancellation`, `…_subnet_cancellation`.
- **Failover & rDNS** — `hetznerrobot_failover`, `hetznerrobot_rdns`; data sources `hetznerrobot_failover(s)`, `hetznerrobot_rdns`, `hetznerrobot_rdns_entries`.
- **Access & boot** — `hetznerrobot_ssh_key`, `hetznerrobot_boot_rescue`, `…_boot_linux`, `…_boot_vnc`, `…_boot_windows`, `hetznerrobot_server_reset`, `hetznerrobot_wake_on_lan`; data sources `hetznerrobot_ssh_key(s)`, `hetznerrobot_boot`, `hetznerrobot_reset(s)`.
- **Firewall** — `hetznerrobot_firewall`, `hetznerrobot_firewall_template`; data sources `hetznerrobot_firewall`, `hetznerrobot_firewall_template(s)`.
- **vSwitch** — `hetznerrobot_vswitch` (servers attached inline); data sources `hetznerrobot_vswitch(es)`.
- **Storage Box** (only the inclusive BX10 — every other box lives in the Hetzner Console API) — `hetznerrobot_storagebox`, `…_storagebox_snapshot`, `…_storagebox_snapshot_plan`, `…_storagebox_subaccount`; data sources `hetznerrobot_storagebox(es)`, `…_storagebox_snapshots`, `…_storagebox_snapshot_plan`, `…_storagebox_subaccounts`.
- **Ordering** — `hetznerrobot_server_order`, `hetznerrobot_server_market_order`, `hetznerrobot_server_addon_order`; data sources `hetznerrobot_server_product(s)`, `hetznerrobot_server_market_product(s)`, `hetznerrobot_server_addon_products`, `hetznerrobot_currency`.
- **Traffic** — data source `hetznerrobot_traffic`.

</details>

<details>
<summary>Deliberately left out</summary>

- **`GET /boot/{n}/rescue/last`, `GET /boot/{n}/linux/last`** — they only repeat the password of the last activation, which the boot resource already holds.
- **Order transaction listings** (`GET /order/*/transaction`) — a 30-day history, not infrastructure; each order resource reads its own transaction.
- **`POST /storagebox/{id}/snapshot/{name}` (revert)** — a destructive one-off that rolls a whole box back; do it consciously in Robot.
- **`GET /wol/{n}`** — only echoes the server's addresses; `hetznerrobot_server.wol` says whether Wake on LAN is available.
- **Domain registration** — not part of the webservice.

</details>

## 🧪 Test status

Every resource and data source runs through acceptance tests against an in-memory mock of the webservice in CI — create, update, import and destroy, including the asynchronous parts (firewall and vSwitch "in process", order transactions) and the error paths (rate limit, 404 collections, conflicts).

| Area | Live-verified | Notes |
| :-- | :-: | :-- |
| `hetznerrobot_server`, `hetznerrobot_ip`, `hetznerrobot_subnet` | ✅ | adopt, rename / change traffic warnings, restore, import |
| `hetznerrobot_rdns`, `hetznerrobot_ssh_key`, `hetznerrobot_firewall_template`, `hetznerrobot_vswitch` | ✅ | create → update → import → destroy |
| `hetznerrobot_boot_rescue`, `hetznerrobot_wake_on_lan` | ✅ | activate → import → deactivate (no reset); WoL packet |
| Data sources: servers, IPs, subnets, rDNS, SSH keys, resets, boot, firewall, firewall templates, vSwitches, failovers, Storage Boxes, traffic, server / IP cancellation info | ✅ | read, including Robot's empty-collection answers |
| `hetznerrobot_server_reset`, `hetznerrobot_firewall` | ❌ **untested** | would disrupt the test account's only server |
| `hetznerrobot_boot_linux`, `…_vnc`, `…_windows`, all `*_cancellation` resources | ❌ **untested** | reinstall or cancel — deliberately never run live |
| `hetznerrobot_failover`, `hetznerrobot_ip_mac`, `hetznerrobot_subnet_mac`, Storage Box resources | ❌ **untested** | the test account has no failover IP, no MAC-capable address and no Storage Box |
| Orders, products, `hetznerrobot_currency` | ❌ **untested** | ordering is not enabled for the test account's webservice user |

The untested parts follow the [official documentation](https://robot.hetzner.com/doc/webservice/en.html) and the behaviour of the verified endpoints. The live suite is safe by design — throwaway entities and settings it restores; it never resets, reinstalls, cancels or reroutes anything:

```bash
TF_ACC=1 HETZNER_ROBOT_LIVE=1 HETZNER_ROBOT_USER=… HETZNER_ROBOT_PASSWORD=… \
  HETZNER_ROBOT_LIVE_SERVER=<server number> HETZNER_ROBOT_LIVE_WRITE=1 HETZNER_ROBOT_LIVE_VSWITCH=1 \
  go test ./internal/provider -run TestLive -v
```

Reports from runs against real accounts are very welcome.

## 📚 Documentation

Per-resource docs live under [`docs/`](docs/), generated from the schema with `make docs` (build + export schema + tfplugindocs).

## 🤝 Contributing

PRs welcome. Conventional Commits required (enforced via commitlint). Husky runs the linters/formatters on `git commit`.

> [!TIP]
> `make test` and `TF_ACC=1 go test ./...` (mock acceptance tests, needs `tofu` on PATH) run without an account.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow.

## 🛣️ Versioning

[Semantic Versioning](https://semver.org/) via [release-please](https://github.com/googleapis/release-please) — see [CHANGELOG.md](CHANGELOG.md).

## 📄 License

[MIT](LICENSE) © [Titus Kirch](https://github.com/TitusKirch/) / [IT-Dienstleistungen Titus Kirch](https://kirch.dev)
