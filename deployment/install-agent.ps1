param(
 [Parameter(Mandatory)][string]$PanelURL,
 [Parameter(Mandatory)][string]$ServerID,
 [Parameter(Mandatory)][string]$TokenFile,
 [string]$AgentExe=(Join-Path (Split-Path $PSScriptRoot -Parent) 'pulse-agent.exe'),
 [string]$StateDirectory='C:\ProgramData\PulseAgent'
)
$ErrorActionPreference='Stop'
if(-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run PowerShell as Administrator'}
$agentPath=(Resolve-Path -LiteralPath $AgentExe).Path
$tokenPath=(Resolve-Path -LiteralPath $TokenFile).Path
$StateDirectory=[IO.Path]::GetFullPath($StateDirectory)
if($StateDirectory.TrimEnd('\') -eq [IO.Path]::GetPathRoot($StateDirectory).TrimEnd('\')){throw 'Install directory must not be a drive root'}
$ancestor=$StateDirectory
while($ancestor){
 if((Test-Path -LiteralPath $ancestor) -and ((Get-Item -LiteralPath $ancestor).Attributes -band [IO.FileAttributes]::ReparsePoint)){throw 'Install directory ancestors must not be links'}
 $ancestor=Split-Path $ancestor -Parent
}
foreach($argument in @($agentPath,$tokenPath,$StateDirectory,$PanelURL,$ServerID)){if($argument.Contains('"') -or $argument.Contains("`n") -or $argument.Contains("`r")){throw 'Arguments must not contain quotes or newlines'}}
if((Get-Content -LiteralPath $tokenPath -Raw).Trim() -notmatch '^[0-9a-fA-F]{64}$'){throw 'Token must contain exactly 64 hexadecimal characters'}
New-Item -ItemType Directory -Force $StateDirectory | Out-Null
& icacls.exe $StateDirectory /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
if($LASTEXITCODE -ne 0){throw 'Failed to protect agent directory'}
$service=Get-Service -Name PulseAgent -ErrorAction SilentlyContinue
if($service){Stop-Service PulseAgent; $service.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))}
$installedExe=Join-Path $StateDirectory 'pulse-agent.exe'
$installedToken=Join-Path $StateDirectory 'agent.token'
if($agentPath -ne $installedExe){Copy-Item -LiteralPath $agentPath -Destination $installedExe -Force}
if($tokenPath -ne $installedToken){Copy-Item -LiteralPath $tokenPath -Destination $installedToken -Force}
& icacls.exe $installedToken /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
if($LASTEXITCODE -ne 0){throw 'Failed to protect token'}
$binary='"'+$installedExe+'" -url "'+$PanelURL+'" -server "'+$ServerID+'" -token-file "'+$installedToken+'" -state "'+$StateDirectory+'"'
if($service){& sc.exe config PulseAgent binPath= $binary start= auto | Out-Null; if($LASTEXITCODE -ne 0){throw 'Service update failed'}}
else {New-Service -Name PulseAgent -DisplayName 'Pulse telemetry agent' -BinaryPathName $binary -StartupType Automatic | Out-Null}
& sc.exe failure PulseAgent reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
if($LASTEXITCODE -ne 0){throw 'Service recovery configuration failed'}
Start-Service PulseAgent
Get-Service PulseAgent