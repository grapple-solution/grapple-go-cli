# install.ps1
# Installation script for Grapple Go CLI on Windows

param (
    [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = 'SilentlyContinue'  # Speeds up downloads

$repo = "grapple-solution/grapple-go-cli"

# Detect architecture
$arch = "amd64"
if ([Environment]::Is64BitOperatingSystem -eq $false) {
    $arch = "386"
    Write-Warning "32-bit systems may not be fully supported."
}

Write-Host "Detected OS: windows, Architecture: $arch"

# Resolve latest version if needed
if ($Version -eq "latest") {
    try {
        $releaseInfo = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
        $Version = $releaseInfo.tag_name
    }
    catch {
        Write-Error "Failed to fetch latest version: $_"
        exit 1
    }
}

# Set download info
$zipFile = "grapple-windows-$arch.zip"
$downloadUrl = "https://github.com/$repo/releases/download/$Version/$zipFile"

# Set installation directories
$tempPath = "$env:TEMP\$zipFile"
$extractPath = "$env:TEMP\grapple-extract"
$binDir = "C:\Program Files\Grapple"
$dataDir = "C:\Program Files\Grapple\share"

# Create extraction directory if it doesn't exist
if (Test-Path $extractPath) {
    Remove-Item -Path $extractPath -Recurse -Force
}
New-Item -ItemType Directory -Path $extractPath -Force | Out-Null

Write-Host "Downloading: $downloadUrl"
try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tempPath -UseBasicParsing
}
catch {
    Write-Error "Download failed: $_"
    exit 1
}

Write-Host "Extracting $zipFile..."
try {
    Expand-Archive -Path $tempPath -DestinationPath $extractPath -Force
    $extractedFolder = Join-Path $extractPath "grapple-windows-$arch"
    
    # Make sure the extracted folder exists
    if (-not (Test-Path $extractedFolder)) {
        $extractedFolder = Get-ChildItem -Path $extractPath -Directory | Select-Object -First 1 -ExpandProperty FullName
    }
}
catch {
    Write-Error "Extraction failed: $_"
    exit 1
}

Write-Host "Installing Grapple CLI..."
try {
    # Create installation directories
    if (-not (Test-Path $binDir)) {
        New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    }
    if (-not (Test-Path $dataDir)) {
        New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    }
    
    # Install executable
    Copy-Item -Path "$extractedFolder\grapple.exe" -Destination $binDir -Force
    
    # Install shared files
    Copy-Item -Path "$extractedFolder\template-files" -Destination $dataDir -Recurse -Force
    Copy-Item -Path "$extractedFolder\files" -Destination $dataDir -Recurse -Force
    
    # Add to PATH if not already there
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    if (-not $currentPath.Contains($binDir)) {
        [Environment]::SetEnvironmentVariable("Path", "$currentPath;$binDir", "Machine")
        Write-Host "Added $binDir to system PATH."
    }
}
catch {
    Write-Error "Installation failed: $_"
    exit 1
}

Write-Host "Cleaning up..."
try {
    Remove-Item -Path $tempPath -Force
    Remove-Item -Path $extractPath -Recurse -Force
}
catch {
    Write-Warning "Cleanup failed: $_"
}

Write-Host "✅ Grapple CLI installed!"
Write-Host "Run 'grapple help' to get started."

# Test if installation was successful
try {
    $grapplePath = Join-Path $binDir "grapple.exe"
    if (Test-Path $grapplePath) {
        # You may need a new PowerShell session to use the updated PATH
        Write-Host "Note: You may need to restart your terminal to use the 'grapple' command."
        Write-Host "Or you can run it directly at: $grapplePath"
    }
    else {
        Write-Error "Something went wrong. Could not find grapple.exe at $grapplePath"
    }
}
catch {
    Write-Warning "Verification failed: $_"
}