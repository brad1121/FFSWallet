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
latest_files=0
if [ "$release_count" -gt 0 ]; then
  latest_tag="$(jq -r '.[0].tag_name' "$releases_json")"
  latest_date="$(date_only "$(jq -r '.[0].published_at // .[0].created_at // ""' "$releases_json")")"
  latest_files="$(jq -r '.[0].assets | length' "$releases_json")"
fi
generated="$(date -u +%Y-%m-%d)"

# ── Stylesheet ───────────────────────────────────────────────────────────────
# The page borrows the wallet's own chrome: a status bar across the top, a
# pipe-separated footer, and the event log as the opening statement. One
# grotesque carries the type, stretched wide for the name and compressed for
# section heads; the mono is for anything a machine wrote (hashes, log lines,
# file names). Colour: a cool grey ground, the app's charcoal for the log and
# code, and the app's blue for the few things that are state.
cat > "$out_dir/styles.css" <<'CSS'
:root {
  --field: #edeeea;
  --field-2: #e2e4df;
  --ink: #14171a;
  --ink-2: #5a6067;
  --rule: #14171a;
  --rule-soft: #c4c8c2;
  --tape: #1b1d21;
  --tape-ink: #d9ddd7;
  --tape-dim: #7c838b;
  --signal: #2f78d6;
  --sans: "Archivo", "Helvetica Neue", Arial, sans-serif;
  --mono: "IBM Plex Mono", ui-monospace, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
}
@media (prefers-color-scheme: dark) {
  :root {
    --field: #15171a;
    --field-2: #1e2125;
    --ink: #e4e7e2;
    --ink-2: #9aa1a8;
    --rule: #e4e7e2;
    --rule-soft: #33373c;
    --tape: #0c0d0f;
    --tape-ink: #d9ddd7;
    --tape-dim: #6f767e;
    --signal: #62a0f0;
  }
}

html { background: var(--field); }
body {
  margin: 0;
  color: var(--ink);
  background: var(--field);
  font-family: var(--sans);
  font-size: 17px;
  line-height: 1.55;
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: underline; text-decoration-thickness: 1.5px; text-underline-offset: 3px; }
a:hover { color: var(--signal); }
code, pre, .mono { font-family: var(--mono); font-variant-numeric: tabular-nums; }
code { font-size: 0.88em; }
p { margin: 0 0 1em; max-width: 65ch; }
small { font-size: inherit; }

.page {
  max-width: 1120px;
  margin: 0 auto;
  padding: 0 clamp(16px, 4vw, 48px) 0;
}

/* Bars: the app has a status bar above and a pipe-separated line below.
   So does the page. */
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 2.4em;
  padding: 12px 0;
  font-family: var(--mono);
  font-size: 13px;
  line-height: 1.5;
  font-variant-numeric: tabular-nums;
  color: var(--ink-2);
}
.bar b { font-weight: 500; color: var(--ink); }
.bar-top { border-bottom: 3px solid var(--rule); }
.bar-bottom {
  margin-top: 88px;
  padding: 14px 0 40px;
  border-top: 3px solid var(--rule);
  gap: 4px 0;
}
.bar-bottom span + span::before { content: " | "; white-space: pre; }

/* Nameplate */
.name {
  padding: 40px 0 8px;
}
.name h1 {
  margin: 0 0 18px -0.04em;
  font-size: clamp(60px, 11.5vw, 148px);
  font-weight: 800;
  font-stretch: 125%;
  line-height: 0.88;
  letter-spacing: -0.035em;
}
.name .lede {
  font-size: clamp(19px, 2vw, 23px);
  line-height: 1.4;
  max-width: 34em;
  margin: 0;
}

/* Event log: what the wallet says while it works, set as the wallet sets it. */
.log {
  margin: 36px 0 0;
  background: var(--tape);
  color: var(--tape-ink);
}
.log-head {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4px 24px;
  padding: 12px 20px;
  border-bottom: 1px solid var(--tape-dim);
  font-family: var(--mono);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.log-head .events { font-family: var(--sans); font-weight: 700; font-size: 14px; letter-spacing: 0.01em; }
.log-body {
  margin: 0;
  padding: 18px 20px 22px;
  font-size: 14px;
  line-height: 1.75;
  overflow-x: auto;
  white-space: pre;
  color: var(--tape-ink);
}
.log-body .t { color: var(--tape-dim); }
.log-body .now { color: #fff; }
.log-body .now .t { color: var(--tape-ink); }
.log-foot {
  margin: 10px 0 0;
  font-size: 14px;
  color: var(--ink-2);
  max-width: 65ch;
}

/* Sections */
section {
  margin-top: 72px;
  padding-top: 14px;
  border-top: 3px solid var(--rule);
}
section > h2 {
  margin: 0 0 22px;
  font-size: clamp(34px, 4.6vw, 52px);
  font-weight: 700;
  font-stretch: 62%;
  line-height: 1;
  letter-spacing: 0.005em;
  text-transform: uppercase;
}
h3 {
  margin: 36px 0 12px;
  font-size: 20px;
  font-weight: 700;
  font-stretch: 88%;
}
.lead { font-size: 18px; }

/* Asset ledger: one file per row, its hash under its name. */
.assets {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  border-top: 2px solid var(--rule);
}
.asset {
  display: grid;
  grid-template-columns: 15rem minmax(0, 1fr) max-content;
  grid-template-areas:
    "plat file meta"
    "plat sha  sha";
  gap: 2px 24px;
  align-items: baseline;
  padding: 14px 0 16px;
  border-bottom: 2px solid var(--rule-soft);
}
.asset .plat {
  grid-area: plat;
  font-size: 20px;
  font-weight: 700;
  font-stretch: 90%;
  line-height: 1.2;
}
.asset .plat small {
  display: block;
  font-size: 14px;
  font-weight: 400;
  font-stretch: 100%;
  color: var(--ink-2);
  margin-top: 2px;
}
.asset .file {
  grid-area: file;
  font-family: var(--mono);
  font-size: 15px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.asset .meta {
  grid-area: meta;
  display: flex;
  gap: 14px;
  align-items: baseline;
  justify-self: end;
  font-family: var(--mono);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  color: var(--ink-2);
  white-space: nowrap;
}
.signed {
  font-family: var(--sans);
  font-stretch: 62%;
  font-weight: 700;
  font-size: 12px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--signal);
  border: 2px solid currentColor;
  padding: 1px 6px 0;
  line-height: 1.3;
}
.asset .sha {
  grid-area: sha;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-2);
  overflow-wrap: anywhere;
  letter-spacing: 0.01em;
}
.asset .sha span { color: var(--ink); margin-right: 0.8em; }

.assets.compact { border-top-width: 1px; margin-top: 0; }
.assets.compact .asset { padding: 8px 0 10px; border-bottom-width: 1px; grid-template-columns: 12rem minmax(0, 1fr) max-content; }
.assets.compact .plat { font-size: 15px; }
.assets.compact .plat small { font-size: 13px; display: inline; margin-left: 0.5em; }
.assets.compact .file { font-size: 13.5px; font-weight: 400; }
.assets.compact .sha { font-size: 11.5px; }
.assets.compact .meta { font-size: 13px; }

details > summary { cursor: pointer; list-style: none; }
details > summary::-webkit-details-marker { display: none; }
details.aux { margin-top: 10px; }
details.aux > summary {
  font-family: var(--mono);
  font-size: 13px;
  color: var(--ink-2);
  padding: 6px 0;
}
details.aux > summary::before { content: "+ "; }
details.aux[open] > summary::before { content: "\2212 "; }

/* Running it / facts */
dl.spec {
  margin: 0;
  display: grid;
  grid-template-columns: 8.5rem minmax(0, 1fr);
  gap: 12px 24px;
  max-width: 72ch;
}
dl.spec dt {
  font-weight: 700;
  font-stretch: 75%;
  font-size: 17px;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  line-height: 1.55;
}
dl.spec dd { margin: 0; }
dl.spec dd p { margin: 0; }

.note { color: var(--ink-2); }

/* Code on the tape */
pre.cmd {
  margin: 20px 0;
  padding: 18px 20px;
  background: var(--tape);
  color: var(--tape-ink);
  font-size: 13.5px;
  line-height: 1.6;
  overflow-x: auto;
  max-width: 100%;
}

/* Status line as the app draws it */
.status {
  display: block;
  width: max-content;
  max-width: 100%;
  box-sizing: border-box;
  overflow-wrap: anywhere;
  margin: 8px 0 20px;
  padding: 8px 14px;
  border: 2px solid var(--rule);
  font-family: var(--mono);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}
.status b { font-weight: 500; color: var(--signal); }

/* Screenshot */
figure.shot { margin: 32px 0 0; }
figure.shot img {
  display: block;
  width: 100%;
  height: auto;
  border: 2px solid var(--rule);
  background: var(--tape);
}
figure.shot figcaption {
  margin-top: 10px;
  font-size: 14px;
  color: var(--ink-2);
  max-width: 65ch;
}

/* Older releases: kept, folded, quiet */
details.release { border-bottom: 1px solid var(--rule-soft); }
details.release:first-of-type { border-top: 2px solid var(--rule); }
details.release > summary {
  display: flex;
  align-items: baseline;
  gap: 20px;
  padding: 12px 0;
}
details.release > summary .tag {
  font-weight: 700;
  font-stretch: 90%;
  font-size: 18px;
  min-width: 5em;
}
details.release > summary .tag::before { content: "+"; display: inline-block; width: 1.1em; color: var(--ink-2); font-weight: 400; }
details.release[open] > summary .tag::before { content: "\2212"; }
details.release > summary .count { font-family: var(--mono); font-size: 13px; color: var(--ink-2); }
details.release > summary .date { margin-left: auto; font-family: var(--mono); font-size: 13px; color: var(--ink-2); font-variant-numeric: tabular-nums; }
details.release > .assets { margin-left: 1.1em; margin-bottom: 18px; }
details.release > .aux { margin-left: 1.1em; margin-bottom: 18px; }

@media (max-width: 720px) {
  body { font-size: 16px; }
  .asset, .assets.compact .asset {
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas: "plat" "file" "meta" "sha";
    gap: 4px 0;
  }
  .asset .meta { justify-self: start; }
  .asset .plat small { display: inline; margin-left: 0.5em; }
  dl.spec { grid-template-columns: 1fr; gap: 4px 0; }
  dl.spec dd { margin-bottom: 12px; }
  details.release > .assets, details.release > .aux { margin-left: 0; }
  .log-body { font-size: 12.5px; }
  .bar { gap: 2px 1.6em; }
}
CSS

# ── Asset list for one release ───────────────────────────────────────────────
# release_list INDEX CLASS writes the asset list for that release, mirroring
# the files into downloads/<tag>/ and hashing each one. CLASS is "" for the
# current release and "compact" for the archive.
release_list() {
  local i="$1" cls="${2:-}"
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

  print_assets "$tag" "$cls" "${main[@]}"
  if [ "${#aux[@]}" -gt 0 ]; then
    printf '<details class="aux"><summary>Signatures and certificates, %d files</summary>\n' "${#aux[@]}"
    print_assets "$tag" "compact" "${aux[@]}"
    printf '</details>\n'
  fi
}

# print_assets TAG CLASS FILE... writes one asset list. Each row is the
# platform, the file, its size, whether a Sigstore signature sits beside it,
# and its full SHA-256.
print_assets() {
  local tag="$1" cls="$2"
  shift 2
  if [ -n "$cls" ]; then
    printf '<ol class="assets %s">\n' "$cls"
  else
    printf '<ol class="assets">\n'
  fi
  local a name size sha platform plat_name plat_form signed
  for a in "$@"; do
    name="$(basename "$a")"
    size="$(wc -c < "$a" | tr -d ' ')"
    sha="$(sha256sum "$a" | cut -d' ' -f1)"
    platform="$(platform_of "$name")"
    case "$name" in
      *.sig) plat_name="Signature"; plat_form="$(platform_of "${name%.sig}")" ;;
      *.pem) plat_name="Certificate"; plat_form="$(platform_of "${name%.pem}")" ;;
      SHA256SUMS) plat_name="Checksums"; plat_form="every file above" ;;
      *)
        if [ -z "$platform" ]; then
          plat_name="File"; plat_form=""
        else
          plat_name="${platform%%,*}"
          plat_form="${platform#*, }"
          [ "$plat_form" = "$platform" ] && plat_form=""
        fi
        ;;
    esac
    signed=""
    if [ -e "$a.sig" ]; then
      signed='<span class="signed">signed</span>'
    fi
    printf '<li class="asset"><div class="plat">%s' "$(html_escape "$plat_name")"
    if [ -n "$plat_form" ]; then
      printf '<small>%s</small>' "$(html_escape "$plat_form")"
    fi
    printf '</div><a class="file" href="downloads/%s/%s">%s</a><div class="meta"><span class="size">%s</span>%s</div><code class="sha"><span>sha256</span>%s</code></li>\n' \
      "$tag" "$(html_escape "$name")" "$(html_escape "$name")" "$(human_size "$size")" "$signed" "$sha"
  done
  printf '</ol>\n'
}

# ── Page ─────────────────────────────────────────────────────────────────────
{
cat <<HTML
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FFSWallet — downloads</title>
<meta name="description" content="FFSWallet: a desktop Bitcoin SV light-node wallet. Downloads, checksums and Sigstore signatures for every release.">
<meta name="color-scheme" content="light dark">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Archivo:ital,wdth,wght@0,62..125,100..900&family=IBM+Plex+Mono:wght@400;500&display=swap">
<link rel="stylesheet" href="styles.css">
</head>
<body>
<div class="page">

<header class="bar bar-top">
HTML
if [ -n "$latest_tag" ]; then
  printf '  <span>Release: <b>%s</b></span><span>Published: <b>%s</b></span><span>Files: <b>%s</b></span><span>Network: <b>Testnet</b></span><span>Platforms: <b>Linux · macOS · Windows</b></span>\n' \
    "$(html_escape "$latest_tag")" "$(html_escape "$latest_date")" "$latest_files"
else
  printf '  <span>Release: <b>none yet</b></span><span>Network: <b>Testnet</b></span>\n'
fi
cat <<'HTML'
</header>

<div class="name">
  <h1>FFSWallet</h1>
  <p class="lede">A desktop Bitcoin SV wallet. It talks to peers, not servers, and your keys live in one encrypted file on your own disk.</p>
</div>

<figure class="log" aria-label="Event log excerpt">
  <div class="log-head"><span class="events">Events</span><span>Height: 1757291 · scanned to 1719168 (38123 behind)</span></div>
<pre class="log-body"><span class="t">16:24:51</span>  wallet unlocked
<span class="t">16:24:51</span>  node started
<span class="t">16:24:52</span>  peer connected: 3.123.101.88:18333
<span class="t">16:24:52</span>  peer connected: 54.152.215.212:18333
<span class="t">16:24:54</span>  peer connected: 104.239.246.158:18333
<span class="t">16:24:55</span>  catching up from h=1719168 block=00000000...3f9a1c72
<span class="now"><span class="t">16:24:58</span>  received tx=9e4b17d0...c2a85f61 vout=0 amount=8774 sat at=mpoi7Croa2C51PkjNNniceRA4k2JrPMaJr</span>
<span class="t">16:25:03</span>  rescan block peer=54.152.215.212:18333 headers=2000 blocks=412 txs=1731 errors=0</pre>
</figure>
<p class="log-foot">The wallet's event log, on testnet. It thinks out loud: which peers it has, where it is scanning from, what it received. The status line at the top compares where the scan has reached with the peers' best height.</p>

<section id="download">
  <h2>Download</h2>
HTML
if [ "$release_count" -eq 0 ]; then
  printf '<p>Nothing has been published yet.</p>\n'
else
  printf '<p class="lead"><a href="%s">%s</a>, published %s. Four builds and a checksum list. Each file shows its SHA-256 here; the Linux builds and <code>SHA256SUMS</code> carry a Sigstore signature.</p>\n' \
    "downloads/$(html_escape "$latest_tag")/" "$(html_escape "$latest_tag")" "$(html_escape "$latest_date")"
  release_list 0 ""
  cat <<'HTML'
<h3>Running it</h3>
<dl class="spec">
  <dt>Linux</dt><dd>Mark the AppImage executable and run it. Or unpack the tarball and run <code>usr/local/bin/ffswallet</code>.</dd>
  <dt>macOS</dt><dd>Unzip and drag FFSWallet into Applications. The app is not notarised, so the first launch is right-click, then Open.</dd>
  <dt>Windows</dt><dd>Run the executable. There is no installer.</dd>
</dl>
<p class="note" style="margin-top:20px">The wallet opens on testnet. Mainnet is a choice you make when you create a wallet.</p>
HTML
fi
cat <<'HTML'
</section>

<section id="verify">
  <h2>Verify</h2>
<p>The release workflow signs the Linux builds and the checksum list with Sigstore, keylessly. There is no public key to fetch: the certificate names the GitHub workflow that produced the file, and cosign checks it against the transparency log. Before you run a download, check it against its <code>.sig</code> and <code>.pem</code>:</p>
HTML
cat <<HTML
<pre class="cmd">cosign verify-blob FFSWallet-linux-amd64.AppImage \\
  --signature   FFSWallet-linux-amd64.AppImage.sig \\
  --certificate FFSWallet-linux-amd64.AppImage.pem \\
  --certificate-identity-regexp '^https://github.com/$(html_escape "$repo")/' \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com</pre>
HTML
cat <<'HTML'
<p>Then <code>sha256sum -c SHA256SUMS</code> in the download directory covers the macOS and Windows files too. <code>SHA256SUMS</code> is itself signed, so the check reaches every file.</p>
</section>

<section id="how">
  <h2>How it syncs</h2>
<p>FFSWallet is a light node. It connects to the Bitcoin SV peer-to-peer network directly, follows block headers, and listens to the mempool for payments to its own addresses. It does not download the chain and it does not trust an indexer.</p>
<p>It only hears what is relayed while it is open. A payment mined while the wallet was closed is not in the balance until that block is replayed. So the wallet remembers the last block it scanned and, on unlock, catches up from there in the background: it pulls each block from peers and replays it. Blocks on this chain run to gigabytes; they stream through one transaction at a time.</p>
<p class="status">Height: 1757291 · scanned to <b>1719168</b> (38123 behind)</p>
<p>That is the status bar. The height on the left is the peers' best; the height in blue is how far the wallet has replayed. When the two agree, the balance is right.</p>
<dl class="spec">
  <dt>Keys</dt><dd>BIP39 seed words, BIP44 derivation. One wallet file, encrypted with Argon2id and AES-GCM.</dd>
  <dt>State</dt><dd>UTXOs and transaction history in a SQLite store beside the wallet file.</dd>
  <dt>Network</dt><dd>Peer-to-peer only. No API keys, no accounts, no telemetry.</dd>
  <dt>Stack</dt><dd>Go, Fyne for the UI, a private BSV SDK for the protocol.</dd>
</dl>
<figure class="shot">
  <img src="home-screen.png" alt="FFSWallet dashboard on testnet: balance, network, peers and height in the status bar; tabs for Dashboard, Receive, Send, UTXOs, History, Settings; the current receive address; and the event log." width="1244" height="837" loading="lazy">
  <figcaption>The dashboard. Balance, network, peer count and height along the top; the receive address and the event log below; wallet name, fee rate and the wallet file's path in the footer.</figcaption>
</figure>
</section>

<section id="releases">
  <h2>Older releases</h2>
HTML
if [ "$release_count" -le 1 ]; then
  printf '<p>Only the current release so far.</p>\n'
else
  printf '<p>Every release stays mirrored here in full. Run the current one; these are for reference.</p>\n'
  for i in $(seq 1 $((release_count - 1))); do
    tag="$(jq -r ".[$i].tag_name" "$releases_json")"
    published="$(date_only "$(jq -r ".[$i].published_at // .[$i].created_at // \"\"" "$releases_json")")"
    count="$(jq -r ".[$i].assets | length" "$releases_json")"
    body="$(jq -r ".[$i].body // \"\"" "$releases_json")"
    body="${body//$'\n'/ }"
    printf '<details class="release"><summary><span class="tag">%s</span><span class="count">%s files</span>' "$(html_escape "$tag")" "$count"
    if [ -n "$body" ]; then
      printf '<span class="body">%s</span>' "$(html_escape "$body")"
    fi
    printf '<span class="date">%s</span></summary>\n' "$(html_escape "$published")"
    release_list "$i" "compact"
    printf '</details>\n'
  done
fi
cat <<HTML
</section>

<footer class="bar bar-bottom">
  <span>Release: <b>$(html_escape "${latest_tag:-none}")</b></span><span>Generated: <b>$generated</b></span><span>Store: <b>downloads/</b>, the release artifacts byte for byte</span>
</footer>

</div>
</body>
</html>
HTML
} > "$out_dir/index.html"

echo "built $out_dir: $release_count release(s), latest ${latest_tag:-none}"
