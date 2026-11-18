# node_latest [SECONDS] prints the newest Node release that is not a
# pre-release, without its v, or nothing when the release list cannot be read
# within SECONDS (30 unless given). It reads the list nvm reads; a pre-release
# (alpha, beta, rc, nightly) carries a hyphen in its version, and nvm's own
# name for the latest, node, takes one when it is the newest entry.
node_latest() {
    local version
    version=$( (curl -fsSL --max-time "${1:-30}" "${NVM_NODEJS_ORG_MIRROR:-https://nodejs.org/dist}/index.tab" 2>/dev/null || true) |
        awk -F'\t' 'NR > 1 && $1 !~ /-/ { print $1; exit }')
    printf '%s' "${version#v}"
}
