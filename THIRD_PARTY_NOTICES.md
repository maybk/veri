# Third-Party Notices

`veri` is licensed under the MIT License (`LICENSE`). It incorporates or depends
on the following pure-Go open-source software under permissive licenses:

---

## 1. Model Context Protocol Go SDK

- **Module**: `github.com/modelcontextprotocol/go-sdk` (`v1.8.0`)
- **Scope**: `internal/mcpserver` (MCP server protocol and transport)
- **License**: Apache-2.0 / MIT License

---

## 2. Cobra & pflag

- **Modules**: `github.com/spf13/cobra` (`v1.10.2`), `github.com/spf13/pflag` (`v1.1.0`)
- **Scope**: `internal/cli` (command-line interface and shell completion)
- **Licenses**: Apache-2.0 (`cobra`), BSD-3-Clause (`pflag`)

---

## 3. BurntSushi TOML

- **Module**: `github.com/BurntSushi/toml` (`v1.6.0`)
- **Scope**: `internal/config` (TOML configuration decoding and strict unknown-key detection)
- **License**: MIT License

---

## 4. Readeck go-readability v2

- **Module**: `codeberg.org/readeck/go-readability/v2` (`v2.1.3`)
- **Scope**: `internal/provider/local` (HTML main article extraction)
- **License**: MIT License

---

## 5. Go Extended Standard Libraries

- **Modules**: `golang.org/x/net`, `golang.org/x/sync`, `golang.org/x/sys`, `golang.org/x/term`, `golang.org/x/text`
- **Scope**: HTML DOM parsing & character encoding detection, concurrency primitives (`errgroup`), Windows UI locale lookup & DPAPI master-key protection, and terminal detection / hidden password input
- **License**: BSD-3-Clause License
