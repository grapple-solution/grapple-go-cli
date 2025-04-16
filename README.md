# 🛠️ Grapple Go CLI

**Grapple Go CLI** is a command-line interface for managing Grapple-based workflows, templates, and infrastructure.

---

## 🚀 Installation

### Homebrew (macOS/Linux)

```bash
brew tap grapple-solution/grapple-go-cli
brew install grapple-go-cli
```

### curl (macOS/Linux)

Install the latest version:

```bash
curl -fsSL https://raw.githubusercontent.com/grapple-solution/grapple-go-cli/main/install.sh | bash
```

Install a specific version:

```bash
VERSION=0.0.15
curl -fsSL https://raw.githubusercontent.com/grapple-solution/grapple-go-cli/main/install.sh | bash -s -- $VERSION
```

The CLI binary will be installed to `/usr/local/bin/grapple`, and the required templates and files will be placed in `/usr/local/share/grapple-go-cli`.

### PowerShell (Windows)

Install the latest version:

```powershell
Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://raw.githubusercontent.com/grapple-solution/grapple-go-cli/main/install.ps1')
```

Install a specific version:

```powershell
$Version = "v0.0.15"; Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://raw.githubusercontent.com/grapple-solution/grapple-go-cli/main/install.ps1')
```

Or download the `install.ps1` script and run it with parameters:

```powershell
.\install.ps1 -Version "v0.0.15"
```

The CLI binary will be installed to `C:\Program Files\Grapple`, and the required templates and files will be placed in `C:\Program Files\Grapple\share`.

---

## 📦 Usage

After installation, run:

```bash
grapple help
```

Common commands:

- `grapple k3d create-install` – Creates new k3d cluster and install grpl on it
- `grapple civo create-install` – Creates new civo cluster and install grpl on it
- `grapple init` – Initialize a new project using predefined grpl-templates

---


## 🤝 Contributing

We welcome contributions of all kinds!

1. Fork this repository
2. Create your feature branch: `git checkout -b feature/my-feature`
3. Commit your changes: `git commit -am 'Add new feature'`
4. Push to the branch: `git push origin feature/my-feature`
5. Open a Pull Request

---

## 📄 License

Licensed under the MIT License.

---

## 🗣️ Support

If you encounter any issues or want to request a feature, please open an issue.

Thanks for using Grapple Go CLI! 🙌
