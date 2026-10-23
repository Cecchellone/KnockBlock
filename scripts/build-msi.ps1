# Builds the Windows tray app and its MSI installer, on Windows (the release
# workflow runs it on a Windows runner).
#
#   scripts/build-msi.ps1 [-Version 1.2.3]     default version: 0.0.0
#
# Needs Go, Git Bash (for scripts/winres.sh) and the WiX v5 .NET tool with
# the UI and Util extensions:
#   dotnet tool install --global wix --version 5.0.2
#   wix extension add -g WixToolset.UI.wixext/5.0.2 WixToolset.Util.wixext/5.0.2
#
# The MSI shows its pages in English or Italian, following the Windows
# display language: it is built once per language (installer/*.wxl), and the
# Italian build is embedded in the English one as a language transform that
# Windows Installer applies by itself. Output: dist/KnockBlock-<version>.msi.
param([string]$Version = "0.0.0")
$ErrorActionPreference = "Stop"

$Version = $Version.TrimStart("v")
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "version must be major.minor.patch, got '$Version'" }
$root = Resolve-Path (Join-Path $PSScriptRoot "..")
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force $dist | Out-Null

Write-Host "==> building knockblock.exe $Version"
bash (Join-Path $root "scripts/winres.sh") $Version
if ($LASTEXITCODE -ne 0) { throw "winres.sh failed" }
Push-Location (Join-Path $root "client")
$env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o (Join-Path $dist "knockblock.exe") ./cmd/knockblock
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Pop-Location

# Languages: culture → LCID. The first is the base package; add a line (and
# installer/<culture>.wxl) for a new language.
$languages = [ordered]@{ "en-US" = 1033; "it-IT" = 1040 }

$work = Join-Path $dist "msi"
New-Item -ItemType Directory -Force $work | Out-Null
foreach ($culture in $languages.Keys) {
  Write-Host "==> building the $culture package"
  # Each "-d name=value" is built as one string: PowerShell would split
  # "name=(expression)" into two arguments.
  $wxs = Join-Path $root "installer/knockblock.wxs"
  $wxl = Join-Path $root "installer/$culture.wxl"
  $exe = Join-Path $dist "knockblock.exe"
  $license = Join-Path $root "LICENSE"
  $icon = Join-Path $dist "knockblock.ico"
  $msi = Join-Path $work "$culture.msi"
  wix build $wxs -arch x64 `
    -ext WixToolset.UI.wixext -ext WixToolset.Util.wixext `
    -culture $culture -loc $wxl `
    -d "Version=$Version" -d "Exe=$exe" -d "License=$license" -d "Icon=$icon" `
    -o $msi
  if ($LASTEXITCODE -ne 0) { throw "wix build ($culture) failed" }
}

$base = ($languages.Keys)[0]
$out = Join-Path $dist "KnockBlock-$Version.msi"
Copy-Item (Join-Path $work "$base.msi") $out -Force

# Embed every other language as a transform named after its LCID, and list
# all LCIDs in the package's Template, so Windows Installer picks the one of
# the user's display language (WiSubStg.vbs / WiLangId.vbs, in PowerShell).
$installer = New-Object -ComObject WindowsInstaller.Installer
foreach ($culture in ($languages.Keys | Select-Object -Skip 1)) {
  $lcid = $languages[$culture]
  $mst = Join-Path $work "$lcid.mst"
  Write-Host "==> embedding $culture ($lcid)"
  $baseMsi = Join-Path $work "$base.msi"
  $langMsi = Join-Path $work "$culture.msi"
  wix msi transform -p -t language $baseMsi $langMsi -o $mst
  if ($LASTEXITCODE -ne 0) { throw "wix msi transform ($culture) failed" }
  $db = $installer.GetType().InvokeMember("OpenDatabase", "InvokeMethod", $null, $installer, @($out, 1))
  $view = $db.GetType().InvokeMember("OpenView", "InvokeMethod", $null, $db, @("SELECT ``Name``,``Data`` FROM _Storages"))
  $rec = $installer.GetType().InvokeMember("CreateRecord", "InvokeMethod", $null, $installer, @(2))
  $rec.GetType().InvokeMember("StringData", "SetProperty", $null, $rec, @(1, "$lcid"))
  $rec.GetType().InvokeMember("SetStream", "InvokeMethod", $null, $rec, @(2, $mst))
  $view.GetType().InvokeMember("Execute", "InvokeMethod", $null, $view, @($rec))
  $view.GetType().InvokeMember("Modify", "InvokeMethod", $null, $view, @(3, $rec)) # msiViewModifyAssign
  $db.GetType().InvokeMember("Commit", "InvokeMethod", $null, $db, $null)
  [void][Runtime.InteropServices.Marshal]::ReleaseComObject($view)
  [void][Runtime.InteropServices.Marshal]::ReleaseComObject($db)
}
$db = $installer.GetType().InvokeMember("OpenDatabase", "InvokeMethod", $null, $installer, @($out, 1))
$sum = $db.GetType().InvokeMember("SummaryInformation", "GetProperty", $null, $db, @(1))
$sum.GetType().InvokeMember("Property", "SetProperty", $null, $sum, @(7, "x64;" + (($languages.Values) -join ",")))
$sum.GetType().InvokeMember("Persist", "InvokeMethod", $null, $sum, $null)
$db.GetType().InvokeMember("Commit", "InvokeMethod", $null, $db, $null)
[void][Runtime.InteropServices.Marshal]::ReleaseComObject($sum)
[void][Runtime.InteropServices.Marshal]::ReleaseComObject($db)
[void][Runtime.InteropServices.Marshal]::ReleaseComObject($installer)

Remove-Item -Recurse -Force $work
Write-Host "Built dist/KnockBlock-$Version.msi"
