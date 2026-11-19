# Baut EasyRadius für Windows (EXE, Verwaltungs-App, MSI) und Linux (tar.gz für amd64/arm64).
#   .\build.ps1 -Version 1.7.0
param(
  [Parameter(Mandatory = $true)]
  [ValidatePattern('^\d+\.\d+\.\d+$')]
  [string]$Version
)
$ErrorActionPreference = "Stop"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) { $env:Path = "C:\Program Files\Go\bin;" + $env:Path }

# Umgebungsvariablen merken, damit ein abgebrochener Build die Konsole nicht verstellt.
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
Push-Location $PSScriptRoot
try {
  Write-Host "Tests ..." -ForegroundColor Cyan
  go test ./...
  if ($LASTEXITCODE -ne 0) { throw "Tests fehlgeschlagen" }

  Write-Host "Baue EasyRadius.exe ..." -ForegroundColor Cyan
  $env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
  go build -trimpath -ldflags "-s -w -X main.version=$Version" -o build\EasyRadius.exe .
  if ($LASTEXITCODE -ne 0) { throw "Build fehlgeschlagen" }
  go build -trimpath -ldflags "-s -w" -o build\radtest.exe .\tools\radtest
  if ($LASTEXITCODE -ne 0) { throw "Build radtest fehlgeschlagen" }

  Write-Host "Baue Linux-Pakete ..." -ForegroundColor Cyan
  foreach ($arch in "amd64", "arm64") {
    $dir = "build\linux\$arch"
    New-Item -ItemType Directory -Force $dir | Out-Null
    $env:GOOS = "linux"; $env:GOARCH = $arch
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o "$dir\easyradius" .
    if ($LASTEXITCODE -ne 0) { throw "Linux-Build ($arch) fehlgeschlagen" }
    go build -trimpath -ldflags "-s -w" -o "$dir\radtest" .\tools\radtest
    if ($LASTEXITCODE -ne 0) { throw "Linux-Build radtest ($arch) fehlgeschlagen" }
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    go run .\tools\pack -o "build\easyradius-$Version-linux-$arch.tar.gz" `
      "$dir\easyradius:0755" "$dir\radtest:0755" "LICENSE:0644" "README.md:0644"
    if ($LASTEXITCODE -ne 0) { throw "Packen ($arch) fehlgeschlagen" }
  }
  $env:GOOS = "windows"; $env:GOARCH = "amd64"

  Write-Host "Baue Verwaltungs-App ..." -ForegroundColor Cyan
  dotnet publish desktop\EasyRadius.Manager\EasyRadius.Manager.csproj -c Release -r win-x64 --self-contained true `
    -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -p:EnableCompressionInSingleFile=true `
    -p:DebugType=none -p:Version=$Version -o build\manager
  if ($LASTEXITCODE -ne 0) { throw "Build der Verwaltungs-App fehlgeschlagen" }

  Write-Host "Baue Installer ..." -ForegroundColor Cyan
  dotnet tool restore
  if ($LASTEXITCODE -ne 0) { throw "dotnet tool restore fehlgeschlagen (WiX)" }
  dotnet tool run wix build installer\EasyRadius.wxs -arch x64 -d Version=$Version `
    -ext WixToolset.Firewall.wixext/5.0.2 -ext WixToolset.Util.wixext/5.0.2 `
    -o "build\EasyRadius-$Version.msi"
  if ($LASTEXITCODE -ne 0) { throw "WiX fehlgeschlagen" }

  Write-Host "Fertig: build\EasyRadius-$Version.msi" -ForegroundColor Green
}
finally {
  foreach ($k in $saved.Keys) {
    if ($null -eq $saved[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue }
    else { Set-Item "env:$k" $saved[$k] }
  }
  Pop-Location
}
