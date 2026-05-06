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

gh api "repos/$repo/releases?per_page=20" > "$releases_json"

cat > "$out_dir/styles.css" <<'EOF'
:root {
  color-scheme: dark;
  --bg: #0b1020;
  --panel: #11182d;
  --panel-2: #18223f;
  --text: #edf2ff;
  --muted: #99a6cc;
  --accent: #79b8ff;
  --accent-2: #f7931a;
  --border: #27345e;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: linear-gradient(180deg, #0b1020 0%, #121b33 100%);
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
  max-width: 1040px;
  margin: 0 auto;
  padding: 32px 20px 64px;
}

.hero {
  padding: 28px;
  border: 1px solid var(--border);
  border-radius: 20px;
  background: rgba(17, 24, 45, 0.92);
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.28);
}

.hero h1 {
  margin: 0 0 12px;
  font-size: 40px;
}

.hero p {
  margin: 8px 0;
  color: var(--muted);
  line-height: 1.6;
}

.grid {
  display: grid;
  gap: 18px;
  margin-top: 24px;
}

.card {
  padding: 22px;
  border: 1px solid var(--border);
  border-radius: 18px;
  background: rgba(24, 34, 63, 0.84);
}

.kicker {
  margin: 0 0 10px;
  color: var(--accent-2);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.release-title {
  margin: 0;
  font-size: 24px;
}

.meta {
  margin: 8px 0 0;
  color: var(--muted);
  font-size: 14px;
}

.assets {
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 10px;
}

.assets li {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: rgba(11, 16, 32, 0.7);
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
}

code {
  padding: 2px 6px;
  border-radius: 8px;
  background: rgba(121, 184, 255, 0.12);
}

@media (max-width: 720px) {
  .hero h1 {
    font-size: 30px;
  }

  .assets li {
    flex-direction: column;
    align-items: flex-start;
  }
}
EOF

cat > "$out_dir/index.html" <<'EOF'
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
    <section class="hero">
      <p class="kicker">Public Downloads</p>
      <h1>FFSWallet Releases</h1>
      <p>Wallet source can stay private. This site publishes built release binaries and signatures only.</p>
      <p>Linux downloads include <code>SHA256SUMS</code> plus Sigstore keyless signatures.</p>
    </section>
EOF

release_count="$(jq 'length' "$releases_json")"
if [ "$release_count" -eq 0 ]; then
  cat >> "$out_dir/index.html" <<'EOF'
    <section class="grid">
      <article class="card">
        <p class="kicker">No Releases</p>
        <h2 class="release-title">Nothing published yet</h2>
        <p class="meta">Run release workflow with a tag like <code>v0.1.0</code>.</p>
      </article>
    </section>
EOF
else
  printf '%s\n' '    <section class="grid">' >> "$out_dir/index.html"
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

    printf '%s\n' '      <article class="card">' >> "$out_dir/index.html"
    printf '        <p class="kicker">%s</p>\n' "$kicker" >> "$out_dir/index.html"
    printf '        <h2 class="release-title">%s</h2>\n' "$(html_escape "$name")" >> "$out_dir/index.html"
    printf '        <p class="meta">Tag <code>%s</code>%s</p>\n' "$tag" "$( [ -n "$published" ] && printf ' • Published %s' "$published" )" >> "$out_dir/index.html"
    if [ -n "$body" ]; then
      printf '        <p class="meta">%s</p>\n' "$(html_escape "$body")" >> "$out_dir/index.html"
    fi
    printf '%s\n' '        <ul class="assets">' >> "$out_dir/index.html"

    shopt -s nullglob
    assets=("$release_dir"/*)
    shopt -u nullglob
    if [ "${#assets[@]}" -eq 0 ]; then
      printf '%s\n' '          <li><span>No assets attached</span><span class="asset-size"></span></li>' >> "$out_dir/index.html"
    else
      for asset_path in "${assets[@]}"; do
        asset_name="$(basename "$asset_path")"
        asset_size="$(wc -c < "$asset_path" | tr -d ' ')"
        printf '          <li><a href="downloads/%s/%s">%s</a><span class="asset-size">%s bytes</span></li>\n' \
          "$tag" "$asset_name" "$asset_name" "$asset_size" >> "$out_dir/index.html"
      done
    fi

    printf '%s\n' '        </ul>' >> "$out_dir/index.html"
    printf '%s\n' '      </article>' >> "$out_dir/index.html"
  done
  printf '%s\n' '    </section>' >> "$out_dir/index.html"
fi

cat >> "$out_dir/index.html" <<EOF
    <p class="footer">Source repo: <a href="https://github.com/$repo">$repo</a></p>
  </main>
</body>
</html>
EOF
