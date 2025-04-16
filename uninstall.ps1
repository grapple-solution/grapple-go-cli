# uninstall.ps1
# Uninstallation script for Grapple Go CLI on Windows

$ErrorActionPreference = "Stop"

# Define installation directories
$binDir = "C:\Program Files\Grapple"
$dataDir = "C:\Program Files\Grapple\share"
$execPath = Join-Path $binDir "grapple.exe"

Write-Host "Uninstalling Grapple CLI..."

# Check if Grapple is installed
if (-not (Test-Path $execPath)) {
    Write-Warning "Grapple CLI doesn't appear to be installed at $execPath"
    $confirmation = Read-Host "Continue with cleanup anyway? (y/n)"
    if ($confirmation -ne 'y') {
        Write-Host "Uninstallation canceled."
        exit 0
    }
}

# Remove from PATH
try {
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    if ($currentPath.Contains($binDir)) {
        $newPath = ($currentPath.Split(';') | Where-Object { $_ -ne $binDir }) -join ';'
        [Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")
        Write-Host "Removed $binDir from system PATH."
    }
}
catch {
    Write-Error "Failed to update PATH: $_"
}

# Remove installation files
try {
    if (Test-Path $binDir) {
        Remove-Item -Path $binDir -Recurse -Force
        Write-Host "Removed installation directory: $binDir"
    }
}
catch {
    Write-Error "Failed to remove installation files: $_"
    Write-Warning "You may need to manually delete $binDir"
}

# Remove any user configuration
$configDir = "$env:USERPROFILE\.grapple"
if (Test-Path $configDir) {
    $confirmation = Read-Host "Would you like to remove Grapple configuration data as well? ($configDir) (y/n)"
    if ($confirmation -eq 'y') {
        try {
            Remove-Item -Path $configDir -Recurse -Force
            Write-Host "Removed configuration directory: $configDir"
        }
        catch {
            Write-Error "Failed to remove configuration data: $_"
            Write-Warning "You may need to manually delete $configDir"
        }
    }
    else {
        Write-Host "Configuration data preserved at $configDir"
    }
}

Write-Host "✅ Grapple CLI has been uninstalled."
Write-Host "Note: You may need to restart your terminal for PATH changes to take effect."