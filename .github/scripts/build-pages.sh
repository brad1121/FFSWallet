#!/usr/bin/env bash
set -euo pipefail

repo="${REPO:?REPO env required}"
out_dir="${1:-public-site}"
releases_json="$(mktemp)"

cleanup() {
  rm -f "$releases_json"
}
trap cleanup EXIT

html_escape() {
  printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'
}

rm -rf "$out_dir"
mkdir -p "$out_dir/downloads"
cp "assets/home-screen.png" "$out_dir/home-screen.png"

gh api "repos/$repo/releases?per_page=20" > "$releases_json"

cat > "$out_dir/styles.css" <<'EOF'
:root {
  color-scheme: dark;
  --bg: #0d1117;
  --bg-2: #121923;
  --panel: #171f2b;
  --panel-2: #1f2a38;
  --panel-3: #0f1620;
  --text: #f5f7fb;
  --muted: #a9b4c8;
  --accent: #7eb6ff;
  --accent-2: #f7931a;
  --border: #2b3648;
  --shadow: 0 24px 80px rgba(0, 0, 0, 0.38);
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background:
    radial-gradient(circle at top left, rgba(247, 147, 26, 0.14), transparent 28%),
    radial-gradient(circle at top right, rgba(126, 182, 255, 0.16), transparent 26%),
    linear-gradient(180deg, var(--bg) 0%, var(--bg-2) 100%);
  color: var(--text);
}

a {
  color: var(--accent);
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
}

.shell {
  max-width: 1180px;
  margin: 0 auto;
  padding: 28px 20px 72px;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: 18px;
  background: rgba(15, 22, 32, 0.82);
  box-shadow: var(--shadow);
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 50%;
  background: linear-gradient(135deg, #ffb34d, var(--accent-2));
  color: #111;
  font-weight: 800;
  box-shadow: 0 10px 30px rgba(247, 147, 26, 0.35);
}

.brand-copy h1 {
  margin: 0;
  font-size: 24px;
}

.brand-copy p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 14px;
}

.toplinks {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.toplinks a {
  color: var(--muted);
  font-size: 14px;
}

.hero {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(300px, 0.65fr);
  gap: 20px;
  margin-bottom: 22px;
}

.hero-main,
.hero-side,
.section-card,
.release-card {
  border: 1px solid var(--border);
  border-radius: 22px;
  background: rgba(23, 31, 43, 0.86);
  box-shadow: var(--shadow);
}

.hero-main {
  overflow: hidden;
}

.hero-copy {
  padding: 30px 30px 20px;
}

.hero-copy h2 {
  margin: 0 0 12px;
  font-size: 48px;
  line-height: 1.05;
}

.hero-copy p {
  margin: 8px 0;
  color: var(--muted);
  line-height: 1.7;
  max-width: 60ch;
}

.cta-row {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 20px;
}

.button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 18px;
  border-radius: 999px;
  border: 1px solid transparent;
  font-weight: 700;
}

.button.primary {
  background: linear-gradient(135deg, #ffb34d, var(--accent-2));
  color: #111;
}

.button.secondary {
  border-color: var(--border);
  background: rgba(15, 22, 32, 0.78);
  color: var(--text);
}

.hero-shot {
  padding: 0 18px 18px;
}

.hero-shot img {
  display: block;
  width: 100%;
  height: auto;
  border-radius: 18px;
  border: 1px solid var(--border);
  background: var(--panel-3);
}

.hero-side {
  display: grid;
  gap: 14px;
  padding: 18px;
  align-content: start;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 330px;
  gap: 20px;
}

.section-card {
  padding: 24px;
}

.section-card + .section-card {
  margin-top: 18px;
}

.release-card {
  padding: 18px;
}

.release-title {
  margin: 0;
  font-size: 24px;
}

.section-title {
  margin: 0 0 12px;
  font-size: 28px;
}

.section-copy {
  margin: 0;
  color: var(--muted);
  line-height: 1.7;
}

.feature-list {
  margin: 18px 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 12px;
}

.feature-list li {
  display: grid;
  grid-template-columns: 42px 1fr;
  gap: 14px;
  padding: 22px;
  border: 1px solid var(--border);
  border-radius: 18px;
  background: rgba(15, 22, 32, 0.68);
}

.feature-icon {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 14px;
  background: rgba(126, 182, 255, 0.12);
  color: var(--accent);
  font-size: 20px;
}

.kicker {
  margin: 0 0 10px;
  color: var(--accent-2);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.meta {
  margin: 8px 0 0;
  color: var(--muted);
  font-size: 14px;
  line-height: 1.6;
}

.assets {
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 12px;
}

.assets li {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: rgba(15, 22, 32, 0.76);
  display: flex;
  justify-content: space-between;
  gap: 16px;
}

.asset-size {
  color: var(--muted);
  white-space: nowrap;
}

.footer {
  margin-top: 28px;
  color: var(--muted);
  font-size: 14px;
  text-align: center;
}

code {
  padding: 2px 6px;
  border-radius: 8px;
  background: rgba(121, 184, 255, 0.12);
}

@media (max-width: 720px) {
  .shell {
    padding-left: 14px;
    padding-right: 14px;
  }

  .topbar,
  .hero,
  .grid {
    grid-template-columns: 1fr;
  }

  .topbar {
    padding: 16px;
  }

  .hero-copy {
    padding: 22px 18px 16px;
  }

  .hero-copy h2 {
    font-size: 34px;
  }

  .assets li {
    flex-direction: column;
    align-items: flex-start;
  }
}
EOF

release_count="$(jq 'length' "$releases_json")"
latest_tag=""
latest_name="No release yet"
latest_published=""
latest_body=""

if [ "$release_count" -gt 0 ]; then
  latest_tag="$(jq -r '.[0].tag_name' "$releases_json")"
  latest_name="$(jq -r '.[0].name // .[0].tag_name' "$releases_json")"
  latest_published="$(jq -r '.[0].published_at // .[0].created_at // ""' "$releases_json")"
  latest_body="$(jq -r '.[0].body // ""' "$releases_json")"
  latest_body="${latest_body//$'\n'/ }"
fi

latest_tag_html="No tag published yet"
if [ -n "$latest_tag" ]; then
  latest_tag_html="Tag <code>$latest_tag</code>"
fi

latest_published_html=""
if [ -n "$latest_published" ]; then
  latest_published_html="<p class=\"meta\">Published $latest_published</p>"
fi

latest_body_html=""
if [ -n "$latest_body" ]; then
  latest_body_html="<p class=\"meta\">$(html_escape "$latest_body")</p>"
fi

cat > "$out_dir/index.html" <<EOF
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>FFSWallet Releases</title>
  <link rel="stylesheet" href="styles.css">
</head>
<body>
  <main class="shell">
    <header class="topbar">
      <div class="brand">
        <div class="brand-mark">F</div>
        <div class="brand-copy">
          <h1>FFSWallet</h1>
        </div>
      </div>
      <nav class="toplinks">
        <a href="#latest">Latest Release</a>
        <a href="#features">Features</a>
        <a href="#downloads">Downloads</a>
      </nav>
    </header>

    <section class="hero">
      <article class="hero-main">
        <div class="hero-copy">
          <p class="kicker">Take Control</p>
          <h2>Bitcoin SV wallet with local control, seed recovery, and downloadable desktop builds.</h2>
          <p>FFSWallet built for Linux, Windows, Mac is a no fuss p2p desktop wallet for BitcoinSV. It supports creating wallets from existing seed words, rescanning for missing UTXOs and handles your sending and receiving of BSV withouth third party services.</p>
          <div class="cta-row">
            <a class="button primary" href="#downloads">Download Latest Release</a>
            <a class="button secondary" href="#features">See Features</a>
          </div>
        </div>
        <div class="hero-shot">
          <img src="home-screen.png" alt="FFSWallet home screen">
        </div>
      </article>
      <aside class="hero-side" id="latest">
        <article class="release-card">
          <p class="kicker">Latest Release</p>
          <h3 class="release-title">$(html_escape "$latest_name")</h3>
          <p class="meta">$latest_tag_html</p>
          $latest_published_html
          $latest_body_html
        </article>
      </aside>
    </section>

    <section class="grid">
      <div>
        <article class="section-card" id="features">
          <p class="kicker">Features</p>
          <h3 class="section-title">Built for straightforward self-custody</h3>
          <p class="section-copy">Desktop-first wallet flow with encrypted local storage, controlled seed import, and release artifacts published for direct download.</p>
          <ul class="feature-list">
            <li><div class="feature-icon">🔐</div><div><strong>Encrypted local wallet file</strong><br><span class="meta">Wallet secrets stored locally with Argon2id and AES-GCM.</span></div></li>
            <li><div class="feature-icon">🌱</div><div><strong>Seed word recovery</strong><br><span class="meta">Restore from existing seed words and choose whether to run a long rescan.</span></div></li>
            <li><div class="feature-icon">⬇</div><div><strong>Direct desktop downloads</strong><br><span class="meta">Linux AppImage, macOS app bundle archive, and raw Windows executable.</span></div></li>
          </ul>
        </article>

        <article class="section-card" id="downloads">
          <p class="kicker">Release Archive</p>
          <h3 class="section-title">Public downloads</h3>
          <p class="section-copy">Choose a release below. Latest release appears first.</p>
EOF

if [ "$release_count" -eq 0 ]; then
  cat >> "$out_dir/index.html" <<'EOF'
          <article class="release-card" style="margin-top:18px;">
            <p class="kicker">No Releases</p>
            <h2 class="release-title">Nothing published yet</h2>
            <p class="meta">Run release workflow with a tag like <code>v0.0.1</code>.</p>
          </article>
EOF
else
  for i in $(seq 0 $((release_count - 1))); do
    tag="$(jq -r ".[$i].tag_name" "$releases_json")"
    name="$(jq -r ".[$i].name // .[$i].tag_name" "$releases_json")"
    published="$(jq -r ".[$i].published_at // .[$i].created_at // \"\"" "$releases_json")"
    body="$(jq -r ".[$i].body // \"\"" "$releases_json")"
    body="${body//$'\n'/ }"
    release_dir="$out_dir/downloads/$tag"
    mkdir -p "$release_dir"
    gh release download "$tag" --repo "$repo" --dir "$release_dir" --pattern '*'

    if [ "$i" -eq 0 ]; then
      kicker="Latest Release"
    else
      kicker="Release"
    fi

    printf '%s\n' '          <article class="release-card" style="margin-top:18px;">' >> "$out_dir/index.html"
    printf '            <p class="kicker">%s</p>\n' "$kicker" >> "$out_dir/index.html"
    printf '            <h2 class="release-title">%s</h2>\n' "$(html_escape "$name")" >> "$out_dir/index.html"
    printf '            <p class="meta">Tag <code>%s</code>%s</p>\n' "$tag" "$( [ -n "$published" ] && printf ' • Published %s' "$published" )" >> "$out_dir/index.html"
    if [ -n "$body" ]; then
      printf '            <p class="meta">%s</p>\n' "$(html_escape "$body")" >> "$out_dir/index.html"
    fi
    printf '%s\n' '            <ul class="assets">' >> "$out_dir/index.html"

    shopt -s nullglob
    assets=("$release_dir"/*)
    shopt -u nullglob
    if [ "${#assets[@]}" -eq 0 ]; then
      printf '%s\n' '              <li><span>No assets attached</span><span class="asset-size"></span></li>' >> "$out_dir/index.html"
    else
      for asset_path in "${assets[@]}"; do
        asset_name="$(basename "$asset_path")"
        asset_size="$(wc -c < "$asset_path" | tr -d ' ')"
        printf '              <li><a href="downloads/%s/%s">%s</a><span class="asset-size">%s bytes</span></li>\n' \
          "$tag" "$asset_name" "$asset_name" "$asset_size" >> "$out_dir/index.html"
      done
    fi

    printf '%s\n' '            </ul>' >> "$out_dir/index.html"
    printf '%s\n' '          </article>' >> "$out_dir/index.html"
  done
fi

cat >> "$out_dir/index.html" <<EOF
        </article>
      </div>
      <aside>
        <article class="section-card">
          <p class="kicker">Verification</p>
          <h3 class="section-title">Check Linux artifacts</h3>
          <p class="section-copy">Download the matching <code>SHA256SUMS</code>, <code>.sig</code>, and <code>.pem</code> files from the latest release, then verify with <code>cosign verify-blob</code>.</p>
        </article>
      </aside>
    </section>

    <p class="footer">Source repo: <a href="https://github.com/$repo">$repo</a></p>
  </main>
</body>
</html>
EOF
