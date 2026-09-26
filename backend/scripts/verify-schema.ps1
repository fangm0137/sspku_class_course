param(
    [Parameter(Mandatory=$true)][string]$MySQLBin,
    [string]$GoCommand = 'go',
    [ValidateSet('schema','csrf')][string]$Suite = 'schema'
)
$ErrorActionPreference = 'Stop'
$backendDir = Split-Path $PSScriptRoot -Parent
$runDir = Join-Path ([IO.Path]::GetTempPath()) ('campusclaw-schema-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $runDir | Out-Null
$dataDir = Join-Path $runDir 'data'
$serverExe = Join-Path $MySQLBin 'mysqld.exe'
$clientExe = Join-Path $MySQLBin 'mysql.exe'
$databaseName = 'campusclaw_schema_verify_' + [guid]::NewGuid().ToString('N')
$testPassword = [guid]::NewGuid().ToString('N')
$serverProcess = $null
$savedEnv = @{}
foreach ($key in @('DB_HOST','DB_PORT','DB_NAME','DB_USER','DB_PASSWORD','TEST_MYSQL8')) {
    $savedEnv[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}

function Invoke-Client([string]$SQL, [string]$Password = '') {
    $info = [Diagnostics.ProcessStartInfo]::new($clientExe)
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    foreach ($arg in @('--no-defaults','--protocol=TCP','--host=127.0.0.1',"--port=$testPort",'--user=root','--batch')) { $info.ArgumentList.Add($arg) }
    $info.Environment.Remove('MYSQL_PWD') | Out-Null
    if ($Password) { $info.Environment['MYSQL_PWD'] = $Password }
    $client = [Diagnostics.Process]::Start($info)
    try {
        $client.StandardInput.WriteLine($SQL)
        $client.StandardInput.Close()
        $stdout = $client.StandardOutput.ReadToEnd()
        $stderr = $client.StandardError.ReadToEnd()
        $client.WaitForExit()
        if ($client.ExitCode -ne 0) { throw 'isolated MySQL client command failed (details suppressed to protect credentials)' }
        return $stdout
    } finally { $client.Dispose() }
}

Push-Location $backendDir
try {
    $version = & $serverExe --no-defaults --version
    if ($version -notmatch 'Ver 8\.0\.') { throw 'Verification requires a MySQL 8.0 server' }
    Write-Output $version
    & $serverExe --no-defaults --initialize-insecure "--datadir=$dataDir" "--log-error=$runDir/initialize.log"
    if ($LASTEXITCODE -ne 0) { throw 'isolated MySQL initialization failed' }
    $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $probe.Start(); $testPort = $probe.LocalEndpoint.Port; $probe.Stop()
    $info = [Diagnostics.ProcessStartInfo]::new($serverExe)
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    foreach ($arg in @('--no-defaults',"--datadir=$dataDir",'--bind-address=127.0.0.1',"--port=$testPort",'--mysqlx=0','--skip-log-bin','--general-log=0',"--log-error=$runDir/server.log")) { $info.ArgumentList.Add($arg) }
    $serverProcess = [Diagnostics.Process]::Start($info)
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        if ($serverProcess.HasExited) { throw 'isolated MySQL server exited before readiness' }
        try { $null = Invoke-Client 'SELECT 1;'; $ready = $true; break } catch { Start-Sleep -Milliseconds 500 }
    }
    if (-not $ready) { throw 'isolated MySQL readiness timeout' }
    # Generated hexadecimal values only; secrets travel on stdin, never command arguments or output.
    $null = Invoke-Client "ALTER USER 'root'@'localhost' IDENTIFIED BY '$testPassword'; CREATE DATABASE $databaseName CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs;"
    $env:DB_HOST = '127.0.0.1'; $env:DB_PORT = [string]$testPort
    $env:DB_NAME = $databaseName; $env:DB_USER = 'root'; $env:DB_PASSWORD = $testPassword
    $env:TEST_MYSQL8 = '1'
    Write-Output 'COMMAND: go run ./cmd/migrate up (first run, empty database)'
    & $GoCommand run ./cmd/migrate up
    if ($LASTEXITCODE -ne 0) { throw 'first migration failed' }
    Write-Output 'COMMAND: go run ./cmd/migrate up (second run)'
    & $GoCommand run ./cmd/migrate up
    if ($LASTEXITCODE -ne 0) { throw 'second migration failed' }
    if ($Suite -eq 'csrf') {
        Write-Output 'COMMAND: go test ./internal/httpapi -run TestMySQL8CSRF -v -count=1'
        & $GoCommand test ./internal/httpapi -run TestMySQL8CSRF -v -count=1
    } else {
        Write-Output 'COMMAND: go test ./internal/schema -run TestMySQL8Schema -v -count=1'
        & $GoCommand test ./internal/schema -run TestMySQL8Schema -v -count=1
    }
    if ($LASTEXITCODE -ne 0) { throw "$Suite integration verification failed" }
    Write-Output "PASS isolated MySQL 8.0 $Suite verification"
} finally {
    if ($null -ne $serverProcess) {
        if (-not $serverProcess.HasExited) {
            try { $null = Invoke-Client 'SHUTDOWN;' $testPassword } catch { }
            if (-not $serverProcess.WaitForExit(10000)) { $serverProcess.Kill(); $serverProcess.WaitForExit() }
        }
        $serverProcess.Dispose()
    }
    foreach ($key in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($key, $savedEnv[$key], 'Process') }
    Pop-Location
    Write-Output 'Isolated server stopped; temporary data retained for inspection, no existing database was modified.'
}
