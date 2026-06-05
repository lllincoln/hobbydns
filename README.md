## 📖 hobbydns

`hobbydns` is **a flexible, developer-friendly private DNS server** designed for home labs, internal corporate networks, and fast-prototyping environments.

Unlike traditional heavyweight DNS software (like BIND9 or Unbound) which require rigid zone structures and full service restarts to apply changes, `hobbydns` exposes everything via **a unified, simple JSON schema**. By running an interactive runtime hot-swap routine, you can modify subdomains, add entirely new top-level internal zones, or toggle your global recursive proxy logic on the fly without breaking a single active network connection.

### 🌟 Key Features

* **🗺️ Multi-Zone Authorization:** Host completely distinct internal environments (e.g., `.lan`, `.internal`, or local company subdomains, or even `.com` domains) simultaneously.
* **⚡ Conditional Upstream Proxy Forwarding:** Acts as a fallback proxy for public domains (like `github.com`) by fetching them from an upstream resolver. Toggle it on and off instantly in the config.
* **🔄 In-Memory Dynamic Hot-Swapping:** Type `reload` in the runtime CLI to reload configuration data safely using thread-safe state synchronization (`sync.RWMutex`).
* **🪶 Lean Architecture:** Built using Go, making it trivial to compile into a single, cross-platform executable binary file.

## 📦 Installation

### Option 1: Use Prebuilt Binaries

Download the executable for your system architecture:

* **Linux amd64:** [hobbydns-linux-amd64](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-linux-amd64)
* **Linux arm64:** [hobbydns-linux-arm64](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-linux-arm64)
* **macOS amd64:** [hobbydns-darwin-amd64](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-darwin-amd64)
* **macOS arm64:** [hobbydns-darwin-arm64](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-darwin-arm64)
* **Windows amd64:** [hobbydns-windows-amd64.exe](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-windows-amd64.exe)
* **Windows arm64:** [hobbydns-windows-arm64.exe](https://github.com/lllincoln/hobbydns/releases/latest/download/hobbydns-windows-arm64.exe)

Ensure you have a `config.json` placed in your working directory (see structural example below). Because binding to standard system network ports (`:53`) requires admin network capabilities, execute with escalated privileges:

```sh
sudo ./hobbydns
```

Or, if you're not using a system port, just run it in user mode:

```sh
./hobbydns
```

### Option 2: Use the Docker Image

Release images are published to GitHub Container Registry as `ghcr.io/lllincoln/hobbydns`.

Pull the latest release image:

```sh
docker pull ghcr.io/lllincoln/hobbydns:latest
```

Run the container with your local `config.json` mounted into `/data`, which is the container working directory:

```sh
docker run --rm -it \
  --name hobbydns \
  -p 53:53/tcp \
  -p 53:53/udp \
  -v "./config.json:/data/config.json:ro" \
  ghcr.io/lllincoln/hobbydns:latest
```

To use a specific release, replace `latest` with the release tag:

```sh
docker run --rm -it \
  --name hobbydns \
  -p 53:53/tcp \
  -p 53:53/udp \
  -v "./config.json:/data/config.json:ro" \
  ghcr.io/lllincoln/hobbydns:v1.0.0
```

If port 53 requires elevated permissions on your host, run Docker with the appropriate privileges for your environment.

## 🚀 Quick Start

### 📄 Create Configuration File

Create your control rules inside a file named `config.json`. Below is a comprehensive blueprint detailing global options, fallback variables, and multi-zone arrays:

```json
{
  "host": "0.0.0.0:53",
  "proxy_fallback": true,
  "upstream_dns": "1.1.1.1:53",
  "zones": [
    {
      "zone_name": "internal.example.com.",
      "master_ns": "ns1.internal.example.com.",
      "records": {
        "": [
          { "type": "A", "ttl": "5m", "values": [{ "address": "10.0.0.10" }] }
        ],
        "ns1": [
          { "type": "A", "ttl": "5m", "values": [{ "address": "10.0.0.10" }] }
        ],
        "devbox": [
          { "type": "A", "ttl": "5m", "values": [{ "address": "10.0.0.100" }] }
        ]
      }
    },
    {
      "zone_name": "example.com.",
      "master_ns": "ns1.example.com.",
      "records": {
        "ns1": [
          { "type": "A", "ttl": "5m", "values": [{ "address": "10.0.0.10" }] }
        ],
        "api": [
          { "type": "A", "ttl": "2m", "values": [{ "address": "10.0.0.150" }] }
        ],
        "staging": [
          { "type": "CNAME", "ttl": "10m", "values": [{ "address": "devbox.internal.example.com." }] }
        ]
      }
    }
  ]
}
```

### 🔄 Hot-Reloading Configs

When changes are applied to your `config.json` file, open the interactive application shell, type `reload`, and hit Enter! No restarts needed:

```text
Running dynamic private DNS on 0.0.0.0:53...

Server engine online. Type 'reload' anytime to apply configurations.
reload
🚀 Configuration and Proxy parameters hot-swapped successfully!
```

When running in Docker, the command above uses a read-only bind mount for `config.json`. Update the host file, then type `reload` in the attached container terminal.

### 🔍 Testing Resolutions

Open a separate terminal shell and execute network diagnostic lookups via `dig` to confirm your custom domain mappings work beautifully:

```sh
# Local internal zone query 🖥️
dig devbox.internal.example.com @127.0.0.1

# External recursive fallback proxy query 🌍
dig github.com @127.0.0.1
```

## 🛑 Troubleshooting

### Address Port 53 Conflicts

By default, modern systemd-based Linux systems run an internal stub resolver via `systemd-resolved` which blocks port 53. Check for conflicts:

```sh
sudo lsof -i :53
```

If active, stop it before executing your server:

```sh
sudo systemctl stop systemd-resolved
```

As a reminder, you can also change the port that `hobbydns` runs on.

## 🎯 Roadmap

* [x] Multi-zone definition support
* [x] Recursive fallback proxying for external routing requests
* [x] Interactive CLI hot-swapping runtime loop
* [ ] Add an automated system file-watcher (`fsnotify`) to handle live reloads instantly on file save without typing
* [ ] Implement integrated HTTP metrics endpoint reporting query statistics and error codes
* [ ] Add local database persistent storage fallbacks via SQLite
* [ ] Support more DNS record types


[Create an issue](https://github.com/lllincoln/hobbydns/issues) if you want to see something new or have a bug that needs to be fixed!

## 🤝 Contributing

**Don't be afraid to contribute!** Whether it's creating an issue or submitting a PR, we are nice people and welcoming to beginners. ☺️

Here's how to get started:

1. Fork the project
2. Clone your fork
3. Create your feature branch (`git checkout -b feature/your-amazing-feature`)
4. Download project dependencies (`go mod download`)
5. Build the project (`go build`)
6. Commit your changes (`git commit -m 'Add an amazing feature'`)
7. Push to the branch (`git push origin feature/your-amazing-feature`)
8. Open a pull request and tell us what your amazing feature is


## 📝 License

Distributed under the MIT License. See `LICENSE.txt` for more details.

## 📬 Contact

👋 **Lincoln Maxwell**: https://lincolnmaxwell.com

🔗 **Project Link**: https://github.com/lllincoln/hobbydns

## 🙏 Acknowledgments

* [miekg/dns](https://github.com/miekg/dns) - Comprehensive, robust DNS library for Go
* [256dpi/newdns](https://github.com/256dpi/newdns) - Clean authoritative DNS server framework
* [Best-README-Template](https://github.com/othneildrew/Best-README-Template) - For the gorgeous markdown layout 
* Our contributors:

<a href="https://github.com/lllincoln/hobbydns/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=lllincoln/hobbydns" alt="contrib.rocks image" />
</a>
