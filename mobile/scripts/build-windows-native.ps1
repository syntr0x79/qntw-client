# Native Windows build of the Quantway desktop client (amd64) with the macOS
# web interface hosted in WebView2. No Docker/WSL: Go, MSYS2 (autotools, NSIS)
# and the Windows-hosted LLVM-MinGW 20261020 are used directly.
#
#   powershell -ExecutionPolicy Bypass -File mobile\scripts\build-windows-native.ps1
#
# Output: bin\quantway-windows-native\Quantway-<version>-amd64-Setup.exe and
# the unpacked install tree in <Work>\package-amd64.
param(
  [string]$Work = "$env:USERPROFILE\qntw-build",
  [string]$Tools = "$env:USERPROFILE\tools",
  [string]$Version = "0.8.1"
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
$go = "$Tools\go\bin\go.exe"
$llvm = "$Tools\llvm-mingw-20261020-ucrt-x86_64"
$bash = "$Tools\msys64\usr\bin\bash.exe"
$makensis = "$Tools\msys64\ucrt64\bin\makensis.exe"
$out = "$repo\bin\quantway-windows-native"
$stage = "$Work\package-amd64"
foreach ($p in $go, "$llvm\bin\x86_64-w64-mingw32-windres.exe", $bash, $makensis) { if (-not (Test-Path $p)) { throw "missing tool: $p" } }
function Posix([string]$p) { '/' + $p.Substring(0,1).ToLower() + ($p.Substring(2) -replace '\\','/') }
function Check { if ($LASTEXITCODE -ne 0) { throw "step failed ($LASTEXITCODE)" } }

if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force "$stage\bin", "$stage\ui", "$stage\assets", "$stage\licenses", $out | Out-Null

# 1. Go binaries ---------------------------------------------------------------
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
$gui = "$repo\mobile\windows\cmd\quantway-gui"
Push-Location "$repo\mobile\windows"
& "$llvm\bin\x86_64-w64-mingw32-windres.exe" launcher.rc -O coff -o "$gui\resource_windows_amd64.syso"; Check
Pop-Location
Push-Location "$repo\mobile"
$ld = '-s -w -H windowsgui'
& $go build -trimpath -ldflags $ld -o "$stage\quantway.exe" ./windows/cmd/quantway-gui; Check
& $go build -trimpath -ldflags $ld -o "$stage\bin\quantway-helper.exe" ./linux/cmd/quantway; Check
foreach ($mode in 'net', 'tinc') {
  & $go build -trimpath -ldflags $ld -o "$stage\bin\quantway-$mode-helper.exe" ./windows/cmd/quantway-vpn-helper; Check
}
Pop-Location

# 2. Patched tinc (Wintun backend) -----------------------------------------------
$tincOut = "$Work\tinc-out"
if (-not (Test-Path "$tincOut\tincd.exe")) {
  $upstream = "$Work\tinc-upstream"
  if (-not (Test-Path "$upstream\.git")) {
    & $bash -lc "git clone -q --depth 1 --branch release-1.1pre18 https://github.com/gsliepen/tinc.git '$(Posix $upstream)'"; Check
  }
  $env:MSYSTEM = 'MSYS'
  & $bash -lc "sh '$(Posix "$repo\mobile\scripts\build-tinc-windows-native.sh")' '$(Posix $repo)' '$(Posix $upstream)' '$(Posix "$Work\tinc-work")' '$(Posix $tincOut)' '$(Posix $llvm)'"; Check
}
Copy-Item "$tincOut\tincd.exe", "$tincOut\tinc.exe" "$stage\bin\"
Copy-Item "$tincOut\COPYING" "$stage\licenses\tinc-COPYING"

# 3. Official signed Wintun --------------------------------------------------------
$wintunZip = "$Work\wintun-0.14.1.zip"
if (-not (Test-Path $wintunZip)) { Invoke-WebRequest -UseBasicParsing https://www.wintun.net/builds/wintun-0.14.1.zip -OutFile $wintunZip }
if ((Get-FileHash $wintunZip -Algorithm SHA256).Hash.ToLower() -ne '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51') { throw 'wintun checksum mismatch' }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::OpenRead($wintunZip)
try {
  foreach ($pair in @(@('wintun/bin/amd64/wintun.dll', "$stage\bin\wintun.dll"), @('wintun/LICENSE.txt', "$stage\licenses\Wintun-LICENSE.txt"))) {
    [IO.Compression.ZipFileExtensions]::ExtractToFile($zip.GetEntry($pair[0]), $pair[1], $true)
  }
} finally { $zip.Dispose() }

# 4. macOS web interface, adapted for Windows -----------------------------------------
$ui = "$repo\mobile\macos\Sources\QuantwayApp\Resources\UI"
Copy-Item "$ui\*" "$stage\ui\" -Recurse
$utf8 = New-Object System.Text.UTF8Encoding($false)
$text = [IO.File]::ReadAllText("$repo\mobile\windows\webui-text.json", $utf8) | ConvertFrom-Json
foreach ($name in $text.files) {
  $path = "$stage\ui\$name"
  $content = [IO.File]::ReadAllText($path, $utf8)
  foreach ($r in $text.replace) { $content = $content.Replace([string]$r[0], [string]$r[1]) }
  [IO.File]::WriteAllText($path, $content, $utf8)
}
# Windows-only stylesheet + Twemoji flag font (Windows cannot draw flag emoji).
$flags = "$Work\TwemojiCountryFlags-0.1.10.woff2"
if (-not (Test-Path $flags)) { Invoke-WebRequest -UseBasicParsing https://cdn.jsdelivr.net/npm/country-flag-emoji-polyfill@0.1.10/dist/TwemojiCountryFlags.woff2 -OutFile $flags }
if ((Get-FileHash $flags -Algorithm SHA256).Hash.ToLower() -ne '9f04f14429bb6a9f415c7a4dd902a918d7e81a4f7526c415496fdb063954e3b8') { throw 'flag font checksum mismatch' }
Copy-Item $flags "$stage\ui\TwemojiCountryFlags.woff2"
Copy-Item "$repo\mobile\windows\webui-windows.css" "$stage\ui\windows.css"
Copy-Item "$repo\mobile\windows\licenses\TwemojiCountryFlags-LICENSE.md" "$stage\licenses\TwemojiCountryFlags-LICENSE.md"
$index = [IO.File]::ReadAllText("$stage\ui\index.html", $utf8)
if (-not $index.Contains('<link rel="stylesheet" href="cabinet-a.css">')) { throw 'index.html stylesheet anchor not found' }
$index = $index.Replace('<link rel="stylesheet" href="cabinet-a.css">', '<link rel="stylesheet" href="cabinet-a.css"><link rel="stylesheet" href="windows.css">')
[IO.File]::WriteAllText("$stage\ui\index.html", $index, $utf8)
Copy-Item "$repo\mobile\windows\quantway.ico" "$stage\assets\quantway.ico"
Copy-Item "$repo\mobile\windows\network\wfp\LICENSE.WireGuard" "$stage\licenses\WireGuard-WFP-LICENSE"
Copy-Item "$repo\mobile\windows\README.txt" "$stage\README.txt"

# 5. Installer (same installer.nsi as the release pipeline) --------------------------------
$setup = "$out\Quantway-$Version-amd64-Setup.exe"
# Windows makensis 3.13 rejects "File /oname" with mixed separators and reads
# BOM-less scripts as ANSI; feed it a backslash, UTF-8-with-BOM copy.
$nsi = [IO.File]::ReadAllText("$repo\mobile\windows\installer.nsi", $utf8).Replace('${BUNDLE}/bin/', '${BUNDLE}\bin\').Replace('${BUNDLE}/assets/', '${BUNDLE}\assets\').Replace('"${BUNDLE}/*"', '"${BUNDLE}\*"')
$nsiCopy = "$Work\installer-native.nsi"
[IO.File]::WriteAllText($nsiCopy, $nsi, (New-Object System.Text.UTF8Encoding($true)))
& $makensis -V2 "-DVERSION=$Version" "-DARCH=amd64" "-DBUNDLE=$stage" "-DOUTPUT=$setup" $nsiCopy; Check
$hash = (Get-FileHash $setup -Algorithm SHA256).Hash.ToLower()
"$hash  $(Split-Path $setup -Leaf)" | Out-File -Encoding ascii "$out\SHA256SUMS"
Write-Host "Installer: $setup"
Write-Host "SHA256:    $hash"
