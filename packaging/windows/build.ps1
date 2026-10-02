# Builds dist\TECHO5-Stream-Deck-Setup-<version>.exe. Needs Go, Node and NSIS (makensis).
# Signing: set WINDOWS_CERT (path to a .pfx) and WINDOWS_CERT_PASSWORD to sign; without them the
# installer is unsigned and SmartScreen warns on first run.
param([string]$Version = "0.0.0")
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..\..")
Push-Location frontend; npm ci; npm run build; Pop-Location
New-Item -ItemType Directory -Force dist\win | Out-Null
$env:CGO_ENABLED = "1"
# -H windowsgui: a tray app, no console window.
go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=v$Version" -o dist\win\techo5-streamdeck.exe .
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o dist\win\deckcap-win.exe .\cmd\deckcap-win
if ($env:WINDOWS_CERT) {
  foreach ($f in "dist\win\techo5-streamdeck.exe", "dist\win\deckcap-win.exe") {
    signtool sign /f $env:WINDOWS_CERT /p $env:WINDOWS_CERT_PASSWORD /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 $f
  }
}
makensis "-DVERSION=$Version" "-DSRC=$PWD\dist\win" packaging\windows\installer.nsi
if ($env:WINDOWS_CERT) {
  signtool sign /f $env:WINDOWS_CERT /p $env:WINDOWS_CERT_PASSWORD /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 "dist\TECHO5-Stream-Deck-Setup-v$Version.exe"
}
