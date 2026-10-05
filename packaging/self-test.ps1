# Runs the app's --self-test and fails if it does. The app is a GUI
# program, so it's started with its output redirected to be seen at all.
param([Parameter(Mandatory)][string]$Exe)
$out = New-TemporaryFile
$p = Start-Process $Exe -ArgumentList '--self-test' -Wait -PassThru -NoNewWindow -RedirectStandardOutput $out
Get-Content $out
Remove-Item $out
if ($p.ExitCode -ne 0) { Write-Error "self-test failed ($($p.ExitCode))"; exit 1 }
