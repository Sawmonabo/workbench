    # Reads only: what is installed is looked up in the directories the
    # installers use, never by running a package manager. In latest mode, when
    # chezmoi would not run this script (WORKBENCH_PROBE_RAN=1), only a missing
    # runtime is work for it: a newer release alone does not make it run, so
    # none is looked up.
    parts=() same=()
    if [ -r "$NVM_DIR/nvm.sh" ]; then
        # shellcheck source=/dev/null
        source "$NVM_DIR/nvm.sh" >/dev/null 2>&1 || true
        node_have=$(nvm version default 2>/dev/null || true); node_have=${node_have#v}; [ "$node_have" = "N/A" ] && node_have=
{{- if eq .versions_mode "pinned" }}
        node_want="{{ .versions.node_default }}"
        node_missing=()
{{- range .versions.node }}
        [ -d "$NVM_DIR/versions/node/v{{ . }}" ] || node_missing+=("{{ . }}")
{{- end }}
{{- else }}
        # nvm version-remote rewrites nvm's lts alias files, and a probe writes nothing:
        # node_latest reads the newest release that is not a pre-release from
        # the same index instead.
        if [ "${WORKBENCH_PROBE_RAN:-}" = 1 ] && [ -n "$node_have" ]; then
            node_want=$node_have
        else
            node_want=$(node_latest 3)
        fi
        node_missing=()
{{- end }}
        if [ -z "$node_want" ]; then
            parts+=("Node is ${node_have:-missing}; the latest version could not be looked up")
        elif [ "$node_have" != "$node_want" ]; then
            parts+=("Node is ${node_have:-missing}, the default will be $node_want")
        elif [ ${#node_missing[@]} -gt 0 ]; then
            printf -v joined '%s, ' "${node_missing[@]}"; parts+=("install Node ${joined%, }")
        else
            same+=("Node $node_have")
        fi
    else
{{- if eq .versions_mode "pinned" }}
        parts+=("install Node {{ join ", " .versions.node }} once nvm is in place, {{ .versions.node_default }} the default")
{{- else }}
        parts+=("install the newest Node once nvm is in place")
{{- end }}
    fi
    if command -v uv >/dev/null 2>&1; then
{{- if eq .versions_mode "pinned" }}
        uv_python="${UV_PYTHON_INSTALL_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/uv/python}"
        python_missing=()
{{- range .versions.python_pinned }}
        compgen -G "$uv_python/cpython-{{ . }}-*" >/dev/null || python_missing+=("{{ . }}")
{{- end }}
        if [ ${#python_missing[@]} -gt 0 ]; then
            printf -v joined '%s, ' "${python_missing[@]}"; parts+=("install Python ${joined%, }")
        else
            same+=("Python {{ join ", " .versions.python_pinned }}")
        fi
{{- else }}
        if [ "${WORKBENCH_PROBE_RAN:-}" = 1 ] && compgen -G "${UV_PYTHON_INSTALL_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/uv/python}/cpython-*" >/dev/null; then
            same+=("Python")
        else
            parts+=("Python is brought to the newest release")
        fi
{{- end }}
    else
{{- if eq .versions_mode "pinned" }}
        parts+=("install Python {{ join ", " .versions.python_pinned }} once uv is in place")
{{- else }}
        parts+=("install the newest Python once uv is in place")
{{- end }}
    fi
    if command -v rustup >/dev/null 2>&1 || [ -x "$HOME/.cargo/bin/rustup" ]; then
        # The default toolchain is rustup's own record; reading it runs nothing.
        rust_default=$(sed -n 's/^default_toolchain *= *"\(.*\)"$/\1/p' "${RUSTUP_HOME:-$HOME/.rustup}/settings.toml" 2>/dev/null || true)
{{- if eq .versions_mode "pinned" }}
        if ! compgen -G "${RUSTUP_HOME:-$HOME/.rustup}/toolchains/{{ .versions.rust }}-*" >/dev/null; then
            parts+=("install Rust {{ .versions.rust }} and make it the default")
        elif [[ "$rust_default" != "{{ .versions.rust }}-"* ]]; then
            parts+=("make Rust {{ .versions.rust }} the default")
        else
            same+=("Rust {{ .versions.rust }}")
        fi
{{- else }}
        if [ "${WORKBENCH_PROBE_RAN:-}" = 1 ] && [[ "$rust_default" == stable-* ]]; then
            same+=("stable Rust")
        elif [[ "$rust_default" == stable-* ]]; then
            parts+=("Rust is brought to the newest stable release")
        else
            parts+=("Rust is brought to the newest stable release and made the default")
        fi
{{- end }}
    else
{{- if eq .versions_mode "pinned" }}
        parts+=("install Rust {{ .versions.rust }} once rustup is in place and make it the default")
{{- else }}
        parts+=("install stable Rust once rustup is in place and make it the default")
{{- end }}
    fi
    if [ ${#parts[@]} -eq 0 ]; then
        printf -v joined '%s, ' "${same[@]}"; echo "runtimes: = ${joined%, } are installed"
    else
        printf -v joined '%s; ' "${parts[@]}"; echo "runtimes: ${joined%; }"
    fi
    exit 0
