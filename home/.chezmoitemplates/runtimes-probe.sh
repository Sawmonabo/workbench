    # Reads only: what is installed is looked up in the directories the
    # installers use, never by running a package manager.
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
        # read the newest release from the same index instead.
        node_want=$(curl -fsSL --max-time 3 "${NVM_NODEJS_ORG_MIRROR:-https://nodejs.org/dist}/index.tab" 2>/dev/null | awk -F'\t' 'NR==2 {print $1}' || true); node_want=${node_want#v}
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
        parts+=("Node can't be installed until nvm is")
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
        parts+=("Python is brought to the newest release")
{{- end }}
    else
        parts+=("Python can't be installed until uv is")
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
        if [[ "$rust_default" == stable-* ]]; then
            parts+=("Rust is brought to the newest stable release")
        else
            parts+=("Rust is brought to the newest stable release and made the default")
        fi
{{- end }}
    else
        parts+=("Rust can't be installed until rustup is")
    fi
    if [ ${#parts[@]} -eq 0 ]; then
        printf -v joined '%s, ' "${same[@]}"; echo "runtimes: = ${joined%, } are installed"
    else
        printf -v joined '%s; ' "${parts[@]}"; echo "runtimes: ${joined%; }"
    fi
    exit 0
