// Build-time only: Node.js + sharp. No Flutter/runtime dependency.
// Usage: node generate-brand-assets.cjs [--sharp /path/to/node_modules/sharp]
// The submitted artwork in assets/branding is read-only. All glyphs come from its SVG paths.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const root = path.resolve(__dirname, '../..');
const moduleIndex = process.argv.indexOf('--sharp');
const sharp = require(moduleIndex < 0 ? 'sharp' : process.argv[moduleIndex + 1]);
const sourceDirectory = 'assets/branding';
const sourceName = `${sourceDirectory}/bluegrass-wordmark-dark-icon.svg`;
const source = fs.readFileSync(path.join(root, sourceName), 'utf8');
const outputs = [];
const sha256 = (bytes) => crypto.createHash('sha256').update(bytes).digest('hex');

function write(relative, bytes) {
  const target = path.join(root, relative);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, bytes);
  outputs.push({ path: relative, sha256: sha256(bytes) });
}

async function render(svg, width, height) {
  // Render vector geometry at the target resolution, never edit a supplied PNG.
  return sharp(Buffer.from(svg), { density: 288 })
    .resize(width, height, { fit: 'fill' }).png().toBuffer();
}

async function main() {
  if (!source.includes('viewBox="0 0 178.5 44"') ||
      !source.includes('transform="translate(-7.25 0)"') ||
      (source.match(/<path\b/g) || []).length !== 10 ||
      /<(?:text|image)\b/.test(source)) {
    throw new Error('Source logo geometry changed; review the asset mapping before regenerating.');
  }
  const grass = source.match(/<g id="grass"[\s\S]*?<\/g>/)?.[0];
  const grassPath = grass?.match(/\bd="([^"]+)"/)?.[1];
  if (!grassPath) throw new Error('Missing original grass path.');
  const dark = source
    .replace('dark icon with dark wordmark', 'dark icon with light wordmark')
    .replace('Wordmark: #182B3A', 'Wordmark: #E8EEF3')
    .replace('id="wordmark" fill="#182b3a"', 'id="wordmark" fill="#e8eef3"');
  const icon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 44 44">
  <rect width="44" height="44" rx="10" fill="#182b3a"/>
  ${grass}
</svg>
`;
  const solidIcon = icon.replace(' rx="10"', '');
  write('docs/branding/wordmark-light.svg', source);
  write('docs/branding/wordmark-dark.svg', dark);
  write('docs/branding/app-icon.svg', icon);
  write('Windows/app/assets/brand.svg', icon);

  // Preserve both submitted 4x PNGs byte-for-byte. The paired SVGs provide the
  // outlined master; light uses the newer cyan icon, dark uses its white text.
  for (const [theme, svg, original] of [
    ['light', source, 'bluegrass-wordmark-dark-icon.png'],
    ['dark', dark, 'bluegrass-wordmark-dark-28@4x.png'],
  ]) {
    const highResolution = fs.readFileSync(path.join(root, sourceDirectory, original));
    const metadata = await sharp(highResolution).metadata();
    if (metadata.width !== 714 || metadata.height !== 176 || !metadata.hasAlpha) {
      throw new Error(`Unexpected submitted PNG dimensions/alpha: ${original}`);
    }
    const standardResolution = await render(svg, 179, 44);
    for (const client of ['Windows/app', 'Android']) {
      write(`${client}/assets/branding/wordmark-${theme}.png`, standardResolution);
      write(`${client}/assets/branding/4.0x/wordmark-${theme}.png`, highResolution);
    }
    for (const client of ['iOS', 'Mac']) {
      write(`${client}/assets/branding/wordmark-${theme}.svg`, svg);
    }
  }
  const icon1024 = await render(solidIcon, 1024, 1024);
  for (const client of ['iOS', 'Mac']) {
    write(`${client}/assets/branding/app-icon-1024.png`, icon1024);
  }

  const sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256];
  const frames = await Promise.all(sizes.map((size) => render(icon, size, size)));
  const header = Buffer.alloc(6 + sizes.length * 16);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(sizes.length, 4);
  let offset = header.length;
  frames.forEach((frame, index) => {
    const entry = 6 + index * 16;
    header[entry] = sizes[index] === 256 ? 0 : sizes[index];
    header[entry + 1] = header[entry];
    header.writeUInt16LE(1, entry + 4);
    header.writeUInt16LE(32, entry + 6);
    header.writeUInt32LE(frame.length, entry + 8);
    header.writeUInt32LE(offset, entry + 12);
    offset += frame.length;
  });
  write('Windows/app/windows/runner/resources/app_icon.ico', Buffer.concat([header, ...frames]));

  const resources = 'Android/android/app/src/main/res';
  for (const [density, size] of Object.entries({ mdpi: 48, hdpi: 72, xhdpi: 96, xxhdpi: 144, xxxhdpi: 192 })) {
    write(`${resources}/mipmap-${density}/ic_launcher.png`, await render(icon, size, size));
  }
  // 50 x 40.1dp glyph centered in the adaptive icon's 108dp viewport; all of
  // its original geometry fits inside Android's central 66dp safe circle.
  write(`${resources}/drawable/ic_bluegrass_foreground.xml`, `<?xml version="1.0" encoding="utf-8"?>
<vector xmlns:android="http://schemas.android.com/apk/res/android"
    android:width="108dp" android:height="108dp"
    android:viewportWidth="108" android:viewportHeight="108">
    <group android:scaleX="0.1171875" android:scaleY="-0.1171875"
        android:translateX="24" android:translateY="84">
        <path android:fillColor="#67D2E5" android:pathData="${grassPath}" />
    </group>
</vector>
`);
  write(`${resources}/values/launcher_colors.xml`, `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="launcher_background">#182B3A</color>
</resources>
`);
  write(`${resources}/mipmap-anydpi-v26/ic_launcher.xml`, `<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/launcher_background" />
    <foreground android:drawable="@drawable/ic_bluegrass_foreground" />
    <monochrome android:drawable="@drawable/ic_bluegrass_foreground" />
</adaptive-icon>
`);
  let iconX = 20;
  const iconPreviews = sizes.map((size) => {
    const nested = icon.replace('<svg ', `<svg x="${iconX}" y="340" width="${size}" height="${size}" `);
    const label = `<text x="${iconX}" y="325" font-size="12" fill="#182b3a">${size}</text>`;
    iconX += size + 16;
    return label + nested;
  }).join('\n');
  const lightPreview = source.replace('<svg ', '<svg x="20" y="10" ')
    .replace('width="714" height="176"', 'width="535.5" height="132"');
  const darkPreview = dark.replace('<svg ', '<svg x="20" y="160" ')
    .replace('width="714" height="176"', 'width="535.5" height="132"');
  const preview = `<svg xmlns="http://www.w3.org/2000/svg" width="840" height="620">
    <rect width="840" height="620" fill="#f4f7f9"/>
    <rect y="150" width="840" height="150" fill="#101c25"/>
    ${lightPreview}${darkPreview}${iconPreviews}
  </svg>`;
  write('docs/branding/preview.png', await render(preview, 840, 620));
  const manifest = {
    source: sourceName,
    sourceSha256: sha256(source),
    renderer: { sharp: sharp.versions.sharp, librsvg: sharp.versions.rsvg },
    variants: {
      light: 'Submitted dark-icon SVG/PNG: #67D2E5 grass, #182B3A wordmark.',
      dark: 'Identical SVG paths and spacing, #E8EEF3 wordmark from the supplied dark PNG.',
      icon: 'Original grass path on #182B3A; no wordmark at launcher/tray sizes.',
      apple: 'Asset staging only; no iOS/macOS app or asset catalog is configured.',
    },
    windowsIconSizes: sizes,
    outputs,
  };
  fs.writeFileSync(path.join(root, 'docs/branding/manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  console.log(`Generated ${outputs.length} branding assets; source submissions were not modified.`);
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
