#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

swift build -c release
app=build/Sheepdog.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$(swift build -c release --show-bin-path)/Sheepdog" "$app/Contents/MacOS/Sheepdog"

cat > "$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>Sheepdog</string>
    <key>CFBundleIdentifier</key>
    <string>com.alwx.sheepdog</string>
    <key>CFBundleName</key>
    <string>Sheepdog</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSMinimumSystemVersion</key>
    <string>15.0</string>
</dict>
</plist>
EOF

codesign --force --sign - "$app"
echo "$app"
