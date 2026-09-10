#!/usr/bin/env bash
# Builds the public downloads site from published GitHub releases.
#
# Output: <out_dir>/index.html, styles.css, latest.json, home-screen.png and
# downloads/<tag>/<asset> for every release. latest.json is read by the wallet
# at startup (internal/update); keep its field names stable.
set -euo pipefail

repo="${REPO:?REPO env required}"
out_dir="${1:-public-site}"
releases_json="$(mktemp)"
trap 'rm -f "$releases_json"' EXIT

html_escape() {
  printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g'
}

# human_size prints bytes as MB with one decimal, or KB/B for small files.
human_size() {
  local b="$1"
  if [ "$b" -ge 1048576 ]; then
    awk -v b="$b" 'BEGIN { printf "%.1f MB", b / 1048576 }'
  elif [ "$b" -ge 1024 ]; then
    awk -v b="$b" 'BEGIN { printf "%.0f KB", b / 1024 }'
  else
    printf '%s B' "$b"
  fi
}

# platform_of names the platform an asset is for, from its file name.
platform_of() {
  case "$1" in
    *linux*amd64*.AppImage) echo "Linux x86-64, AppImage" ;;
    *linux*amd64*.tar.xz) echo "Linux x86-64, tarball" ;;
    *linux*arm64*.AppImage) echo "Linux arm64, AppImage" ;;
    *linux*arm64*.tar.xz) echo "Linux arm64, tarball" ;;
    *macos*arm64*) echo "macOS, Apple silicon" ;;
    *macos*amd64*) echo "macOS, Intel" ;;
    *windows*amd64*) echo "Windows x86-64" ;;
    SHA256SUMS) echo "Checksums" ;;
    *) echo "" ;;
  esac
}

# date_only trims an ISO timestamp to its date.
date_only() {
  printf '%s' "${1%%T*}"
}

rm -rf "$out_dir"
mkdir -p "$out_dir/downloads"
cp "assets/home-screen.png" "$out_dir/home-screen.png"

gh api "repos/$repo/releases?per_page=20" > "$releases_json"
release_count="$(jq 'length' "$releases_json")"

# latest.json is what the wallet reads at startup to tell whether it is
# behind: keep the field names stable (internal/update in the wallet).
jq '.[0] // {} | {
  tag: (.tag_name // ""),
  version: ((.tag_name // "") | ltrimstr("v")),
  name: (.name // .tag_name // ""),
  published_at: (.published_at // .created_at // ""),
  url: (.html_url // "")
}' "$releases_json" > "$out_dir/latest.json"

latest_tag=""
latest_date=""
if [ "$release_count" -gt 0 ]; then
  latest_tag="$(jq -r '.[0].tag_name' "$releases_json")"
  latest_date="$(date_only "$(jq -r '.[0].published_at // .[0].created_at // ""' "$releases_json")")"
fi

# ── Stylesheet ───────────────────────────────────────────────────────────────
cat > "$out_dir/styles.css" <<'CSS'
/* FFSWallet release sheet. Paper, ink, one accent. No decoration that does
   not carry information. */
:root {
  --paper: #f4f1ea;
  --paper-2: #ebe7dd;
  --ink: #17170f;
  --ink-2: #4a4939;
  --rule: #17170f;
  --rule-soft: #cdc8ba;
  --accent: #d98e04;
  --mono: ui-monospace, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
  --serif: "Iowan Old Style", "Palatino Linotype", Palatino, "Book Antiqua", "URW Palladio L", Georgia, serif;
}
@media (prefers-color-scheme: dark) {
  :root {
    --paper: #161612;
    --paper-2: #1e1e19;
    --ink: #ebe6d8;
    --ink-2: #a8a394;
    --rule: #ebe6d8;
    --rule-soft: #3a3930;
    --accent: #e9a20a;
  }
}

html { background: var(--paper); }
body {
  margin: 0;
  color: var(--ink);
  background: var(--paper);
  font-family: var(--serif);
  font-size: 17px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: underline; text-decoration-thickness: 1px; text-underline-offset: 3px; }
a:hover { text-decoration-color: var(--accent); }
code, pre, .mono, table { font-family: var(--mono); }
code { font-size: 0.88em; }
pre {
  margin: 0;
  padding: 14px 16px;
  font-size: 13.5px;
  line-height: 1.55;
  overflow-x: auto;
  background: var(--paper-2);
  border-left: 3px solid var(--ink);
}

.sheet {
  max-width: 880px;
  margin: 0 auto;
  padding: 40px 24px 96px;
}

/* Masthead */
.masthead {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  padding-bottom: 14px;
  border-bottom: 2px solid var(--rule);
}
.masthead h1 {
  margin: 0;
  font-size: 56px;
  line-height: 1;
  letter-spacing: -0.02em;
  font-weight: 600;
}
.masthead h1::before {
  content: "";
  display: inline-block;
  width: 0.42em;
  height: 0.42em;
  margin-right: 0.28em;
  background: var(--accent);
  vertical-align: 0.05em;
}
.masthead .sub {
  margin: 10px 0 0;
  color: var(--ink-2);
  font-size: 17px;
}
.masthead .version {
  text-align: right;
  font-family: var(--mono);
  font-size: 13px;
  line-height: 1.6;
  color: var(--ink-2);
  white-space: nowrap;
}
.masthead .version strong { color: var(--ink); font-weight: 600; font-size: 15px; }

/* Screenshot */
figure {
  margin: 28px 0 0;
}
figure img {
  display: block;
  width: 100%;
  border: 1px solid var(--rule);
}
figcaption {
  margin-top: 8px;
  font-family: var(--mono);
  font-size: 12.5px;
  color: var(--ink-2);
}

/* Sections: numbered, hairline above, label in the margin on wide screens */
section {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  gap: 0 32px;
  margin-top: 40px;
  padding-top: 18px;
  border-top: 1px solid var(--rule);
}
section > h2 {
  grid-column: 1;
  margin: 0;
  font-family: var(--mono);
  font-size: 12.5px;
  font-weight: 500;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--ink-2);
  line-height: 1.8;
}
section > h2 .num { display: block; color: var(--ink); font-size: 20px; letter-spacing: 0; font-family: var(--serif); font-weight: 600; }
section > .body { grid-column: 2; min-width: 0; }
.body > :first-child { margin-top: 0; }
.body p { margin: 0 0 14px; max-width: 62ch; }
.body h3 { margin: 22px 0 8px; font-size: 20px; font-weight: 600; }

/* Tables */
.tablewrap { overflow-x: auto; }
table {
  width: 100%;
  table-layout: fixed;
  border-collapse: collapse;
  font-size: 13.5px;
  line-height: 1.45;
}
col.c-platform { width: 24%; }
col.c-file { width: 34%; }
col.c-size { width: 10%; }
col.c-sha { width: 32%; }
th, td {
  padding: 8px 10px 8px 0;
  text-align: left;
  vertical-align: top;
  border-bottom: 1px solid var(--rule-soft);
}
th {
  font-weight: 500;
  color: var(--ink-2);
  border-bottom: 1px solid var(--rule);
}
td, th { overflow-wrap: anywhere; }
td.platform { font-family: var(--serif); font-size: 15.5px; }
td.size { white-space: nowrap; color: var(--ink-2); }
td.sha { color: var(--ink-2); }
td.sha code { font-size: 11.5px; }
tr.aux td { color: var(--ink-2); }
tr.aux td.platform { font-size: 14px; }
details.aux { border: 0; margin-top: 6px; }
details.aux summary { font-size: 12.5px; color: var(--ink-2); padding: 8px 0; }
details.aux .tablewrap { padding: 0 0 6px 0; }

/* Archive */
details { border-bottom: 1px solid var(--rule-soft); }
details summary {
  cursor: pointer;
  padding: 12px 0;
  font-family: var(--mono);
  font-size: 13.5px;
  list-style: none;
  display: flex;
  gap: 18px;
}
details summary::-webkit-details-marker { display: none; }
details summary::before { content: "+"; width: 1em; color: var(--ink-2); }
details[open] summary::before { content: "\2212"; }
details summary .date { color: var(--ink-2); margin-left: auto; }
details .tablewrap { padding: 0 0 18px 2.1em; }

dl { margin: 0; display: grid; grid-template-columns: max-content 1fr; gap: 6px 18px; font-size: 15px; }
dt { font-family: var(--mono); font-size: 13px; color: var(--ink-2); padding-top: 2px; }
dd { margin: 0; }

.colophon {
  margin-top: 56px;
  padding-top: 14px;
  border-top: 2px solid var(--rule);
  font-family: var(--mono);
  font-size: 12.5px;
  color: var(--ink-2);
  display: flex;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

@media (max-width: 640px) {
  body { font-size: 16px; }
  .masthead { flex-direction: column; align-items: flex-start; gap: 12px; }
  .masthead h1 { font-size: 40px; }
  .masthead .version { text-align: left; }
  section { grid-template-columns: 1fr; gap: 10px; }
  section > h2, section > .body { grid-column: 1; }
  section > h2 .num { display: inline; margin-right: 10px; font-size: 16px; }
  details .tablewrap { padding-left: 0; }
  col.c-platform { width: 40%; }
  col.c-file { width: 42%; }
  col.c-size { width: 18%; }
  col.c-sha, th.sha, td.sha { display: none; }
  td.platform { font-size: 14px; }
}
CSS

# ── Download table for one release ───────────────────────────────────────────
# release_table INDEX writes a <table> of that release's assets, mirroring them
# into downloads/<tag>/ and hashing each one for the SHA-256 column.
release_table() {
  local i="$1"
  local tag release_dir
  tag="$(jq -r ".[$i].tag_name" "$releases_json")"
  release_dir="$out_dir/downloads/$tag"
  mkdir -p "$release_dir"
  gh release download "$tag" --repo "$repo" --dir "$release_dir" --pattern '*'

  shopt -s nullglob
  local assets=("$release_dir"/*)
  shopt -u nullglob

  if [ "${#assets[@]}" -eq 0 ]; then
    printf '<p class="mono">No files attached to this release.</p>\n'
    return
  fi

  # Binaries first, in a fixed platform order, then the checksum list; the
  # signatures and certificates go in a fold below so the four downloads are
  # what the eye lands on.
  local main=() aux=()
  local pattern a n
  for pattern in '*.AppImage' '*linux*.tar.xz' '*macos*' '*windows*' 'SHA256SUMS'; do
    for a in "${assets[@]}"; do
      n="$(basename "$a")"
      case "$n" in
        *.sig|*.pem) continue ;;
      esac
      # shellcheck disable=SC2254
      case "$n" in
        $pattern) main+=("$a") ;;
      esac
    done
  done
  for a in "${assets[@]}"; do
    n="$(basename "$a")"
    case "$n" in
      *.sig|*.pem) aux+=("$a") ;;
    esac
  done

  print_table "$tag" "${main[@]}"
  if [ "${#aux[@]}" -gt 0 ]; then
    printf '<details class="aux"><summary>Signatures and certificates, %d files</summary>\n' "${#aux[@]}"
    print_table "$tag" "${aux[@]}"
    printf '</details>\n'
  fi
}

# print_table TAG FILE... writes one download table.
print_table() {
  local tag="$1"
  shift
  printf '<div class="tablewrap"><table>\n'
  printf '<colgroup><col class="c-platform"><col class="c-file"><col class="c-size"><col class="c-sha"></colgroup>\n'
  printf '<thead><tr><th>Platform</th><th>File</th><th>Size</th><th class="sha">SHA-256</th></tr></thead>\n<tbody>\n'
  local a name size sha platform cls
  for a in "$@"; do
    name="$(basename "$a")"
    size="$(wc -c < "$a" | tr -d ' ')"
    sha="$(sha256sum "$a" | cut -d' ' -f1)"
    platform="$(platform_of "$name")"
    cls=""
    case "$name" in
      *.sig) platform="Signature, $(platform_of "${name%.sig}")"; cls=' class="aux"' ;;
      *.pem) platform="Certificate, $(platform_of "${name%.pem}")"; cls=' class="aux"' ;;
      SHA256SUMS) cls=' class="aux"' ;;
    esac
    printf '<tr%s><td class="platform">%s</td><td><a href="downloads/%s/%s">%s</a></td><td class="size">%s</td><td class="sha"><code>%s</code></td></tr>\n' \
      "$cls" "$(html_escape "$platform")" "$tag" "$(html_escape "$name")" "$(html_escape "$name")" "$(human_size "$size")" "$sha"
  done
  printf '</tbody></table></div>\n'
}

# ── Page ─────────────────────────────────────────────────────────────────────
{
cat <<HTML
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FFSWallet — releases</title>
<meta name="description" content="FFSWallet: a desktop Bitcoin SV wallet. Downloads, checksums and signatures for every release.">
<meta name="color-scheme" content="light dark">
<link rel="stylesheet" href="styles.css">
</head>
<body>
<main class="sheet">

<header class="masthead">
  <div>
    <h1>FFSWallet</h1>
    <p class="sub">A desktop Bitcoin SV wallet. Your keys stay in an encrypted file on your own machine.</p>
  </div>
  <div class="version">
HTML
if [ -n "$latest_tag" ]; then
  printf '    <strong>%s</strong><br>released %s<br>Linux · macOS · Windows\n' "$(html_escape "$latest_tag")" "$(html_escape "$latest_date")"
else
  printf '    <strong>no release yet</strong>\n'
fi
cat <<HTML
  </div>
</header>

<figure>
  <img src="home-screen.png" alt="FFSWallet dashboard: balance, network, peers and chain height across the top; receive address and a live event log below." width="1244" height="837">
  <figcaption>Dashboard on testnet. The event log is the wallet thinking out loud: peers, payments, scan progress.</figcaption>
</figure>

<section id="download">
  <h2><span class="num">01</span>Download</h2>
  <div class="body">
HTML
if [ "$release_count" -eq 0 ]; then
  printf '<p>Nothing has been published yet.</p>\n'
else
  printf '<p>Current release, <a href="%s">%s</a>. Pick your platform. The Linux files are signed; every file has its SHA-256 beside it, and all of them are listed in <code>SHA256SUMS</code>.</p>\n' \
    "downloads/$(html_escape "$latest_tag")/" "$(html_escape "$latest_tag")"
  release_table 0
  cat <<'HTML'
<h3>Running it</h3>
<p><span class="mono">Linux</span> — make the AppImage executable and run it, or unpack the tarball and run <code>usr/local/bin/ffswallet</code>. <span class="mono">macOS</span> — unzip and drag the app into Applications; it is not notarised, so the first launch needs a right-click → Open. <span class="mono">Windows</span> — run the executable; there is no installer.</p>
<p>The wallet opens on testnet. Mainnet is a choice at wallet creation.</p>
HTML
fi
cat <<'HTML'
  </div>
</section>

<section id="verify">
  <h2><span class="num">02</span>Verify</h2>
  <div class="body">
<p>Linux artifacts and the checksum list are signed keylessly with Sigstore from the release workflow. Check a download against its <code>.sig</code> and <code>.pem</code> before running it:</p>
HTML
cat <<HTML
<pre>cosign verify-blob FFSWallet-linux-amd64.AppImage \\
  --signature   FFSWallet-linux-amd64.AppImage.sig \\
  --certificate FFSWallet-linux-amd64.AppImage.pem \\
  --certificate-identity-regexp '^https://github.com/$(html_escape "$repo")/' \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com</pre>
HTML
cat <<'HTML'
<p>Then <code>sha256sum -c SHA256SUMS</code> in the download directory covers the macOS and Windows files too, since <code>SHA256SUMS</code> is itself signed.</p>
  </div>
</section>

<section id="how">
  <h2><span class="num">03</span>How it works</h2>
  <div class="body">
<p>FFSWallet is a light node. It talks to the Bitcoin SV peer-to-peer network directly, follows block headers, and listens to the mempool for payments to its own addresses. It does not download the chain and it does not trust an indexer.</p>
<p>Because it only hears what is relayed while it is open, a payment mined while the wallet was closed is not in the balance until that block is replayed. So the wallet remembers the last block it scanned, and on unlock it catches up from there in the background, pulling each block from peers and replaying it. Blocks on this chain can run to gigabytes; those stream through one transaction at a time.</p>
<p>The status bar shows the height the wallet has scanned to next to the peers' best height. When the two agree, the balance is right.</p>
<dl>
  <dt>keys</dt><dd>BIP39 seed words, BIP44 derivation. Kept in one file encrypted with Argon2id and AES-GCM.</dd>
  <dt>state</dt><dd>UTXOs and transaction history in a local SQLite store beside the wallet file.</dd>
  <dt>network</dt><dd>Peer-to-peer only. No API keys, no accounts, no telemetry.</dd>
  <dt>stack</dt><dd>Go, Fyne for the UI, a private BSV SDK for the protocol.</dd>
</dl>
  </div>
</section>

<section id="releases">
  <h2><span class="num">04</span>All releases</h2>
  <div class="body">
HTML
if [ "$release_count" -le 1 ]; then
  printf '<p>Only the current release so far.</p>\n'
else
  printf '<p>Every release is mirrored here in full, newest first. Older builds are kept for reference; run the current one.</p>\n'
  for i in $(seq 1 $((release_count - 1))); do
    tag="$(jq -r ".[$i].tag_name" "$releases_json")"
    published="$(date_only "$(jq -r ".[$i].published_at // .[$i].created_at // \"\"" "$releases_json")")"
    body="$(jq -r ".[$i].body // \"\"" "$releases_json")"
    body="${body//$'\n'/ }"
    printf '<details><summary><span>%s</span>' "$(html_escape "$tag")"
    if [ -n "$body" ]; then
      printf '<span>%s</span>' "$(html_escape "$body")"
    fi
    printf '<span class="date">%s</span></summary>\n' "$(html_escape "$published")"
    release_table "$i"
    printf '</details>\n'
  done
fi
cat <<HTML
  </div>
</section>

<footer class="colophon">
  <span>FFSWallet · built from tag $(html_escape "${latest_tag:-—}") · page generated $(date -u +%Y-%m-%d)</span>
  <span>Files on this page are the release artifacts, byte for byte. Nothing else is served.</span>
</footer>

</main>
</body>
</html>
HTML
} > "$out_dir/index.html"

echo "built $out_dir: $release_count release(s), latest ${latest_tag:-none}"
