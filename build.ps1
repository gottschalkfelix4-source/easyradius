# Baut EasyRadius.exe und den Installer (MSI).
#   .\build.ps1 -Version 1.0.0
param([string]$Version = "1.0.0")
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not (Get-Command go -ErrorAction SilentlyContinue)) { $env:Path = "C:\Program Files\Go\bin;" + $env:Path }

Write-Host "Tests ..." -ForegroundColor Cyan
go test ./...
if ($LASTEXITCODE -ne 0) { throw "Tests fehlgeschlagen" }

Write-Host "Baue EasyRadius.exe ..." -ForegroundColor Cyan
$env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o build\EasyRadius.exe .
if ($LASTEXITCODE -ne 0) { throw "Build fehlgeschlagen" }
go build -trimpath -ldflags "-s -w" -o build\radtest.exe .\tools\radtest
if ($LASTEXITCODE -ne 0) { throw "Build radtest fehlgeschlagen" }

Write-Host "Baue Verwaltungs-App ..." -ForegroundColor Cyan
dotnet publish desktop\EasyRadius.Manager\EasyRadius.Manager.csproj -c Release -r win-x64 --self-contained true `
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -p:EnableCompressionInSingleFile=true `
  -p:DebugType=none -p:Version=$Version -o build\manager
if ($LASTEXITCODE -ne 0) { throw "Build der Verwaltungs-App fehlgeschlagen" }

Write-Host "Baue Installer ..." -ForegroundColor Cyan
dotnet tool restore | Out-Null
dotnet tool run wix build installer\EasyRadius.wxs -arch x64 -d Version=$Version `
  -ext WixToolset.Firewall.wixext/5.0.2 -ext WixToolset.Util.wixext/5.0.2 `
  -o "build\EasyRadius-$Version.msi"
if ($LASTEXITCODE -ne 0) { throw "WiX fehlgeschlagen" }

Write-Host "Fertig: build\EasyRadius-$Version.msi" -ForegroundColor Green
