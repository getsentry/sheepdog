#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

swift build -c release
app=build/ReviewQueue.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$(swift build -c release --show-bin-path)/ReviewQueue" "$app/Contents/MacOS/ReviewQueue"

cat > "$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>ReviewQueue</string>
    <key>CFBundleIdentifier</key>
    <string>com.alwx.reviewqueue</string>
    <key>CFBundleName</key>
    <string>Review Queue</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSMinimumSystemVersion</key>
    <string>15.0</string>
</dict>
</plist>
EOF

codesign --force --sign - "$app"
echo "$app"
