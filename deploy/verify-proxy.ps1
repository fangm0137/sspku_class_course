param(
    [string]$DockerCommand = 'docker',
    [string]$DockerConfigDirectory = '',
    [switch]$VerifyPersistence
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$project = 'campusclaw-t7-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$names = @('DOCKER_CONFIG','TOKEN_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD','DB_NAME','DB_USER','CAMPUSCLAW_PORT','SEED_TEACHER_A_PASSWORD','SEED_STUDENT_A1_PASSWORD','SEED_STUDENT_B1_PASSWORD')
$saved = @{}
$emptyEnv = New-TemporaryFile
$directory = Join-Path ([IO.Path]::GetTempPath()) $project
New-Item -ItemType Directory -Path $directory | Out-Null
$composeArgs = @('compose','--env-file',$emptyEnv.FullName,'-f',(Join-Path $root 'compose.yaml'),'-p',$project)
function Invoke-Compose {
    & $DockerCommand @composeArgs @args
    if ($LASTEXITCODE -ne 0) { throw 'Compose command failed; inspect the preceding non-secret output' }
}
function Request([string]$Method, [string]$Path, $Session, [int]$Status, $Body = $null, [string]$Token = '') {
    $parameters = @{Uri="$base$Path";Method=$Method;SkipHttpErrorCheck=$true;TimeoutSec=45}
    if ($Session) { $parameters.WebSession = $Session }
    if ($Token) { $parameters.Headers = @{'X-CSRF-Token'=$Token;Origin=$base} }
    if ($null -ne $Body) { $parameters.Body=$Body; $parameters.ContentType='application/json' }
    $response = Invoke-WebRequest @parameters
    if ([int]$response.StatusCode -ne $Status) { throw "$Method $Path expected $Status, received $($response.StatusCode)" }
    if ($Path -ne '/' -and $response.Headers.'Content-Type' -notmatch 'application/json') { throw "$Path returned non-JSON content" }
    return $response
}
function Login([string]$Username, [string]$Password) {
    $session = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $token = (Request GET '/api/csrf' $session 200).Content | ConvertFrom-Json
    $body = @{username=$Username;password=$Password} | ConvertTo-Json -Compress
    $null = Request POST '/api/login' $session 200 $body $token.csrf_token
    return $session
}
try {
    foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    if ($DockerConfigDirectory) { $env:DOCKER_CONFIG=$DockerConfigDirectory }
    foreach ($name in @('TOKEN_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD','SEED_TEACHER_A_PASSWORD','SEED_STUDENT_A1_PASSWORD','SEED_STUDENT_B1_PASSWORD')) {
        [Environment]::SetEnvironmentVariable($name,[guid]::NewGuid().ToString('N'),'Process')
    }
    $env:DB_NAME='campusclaw'; $env:DB_USER='campusclaw'
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
    $listener.Start(); $env:CAMPUSCLAW_PORT=[string]$listener.LocalEndpoint.Port; $listener.Stop()
    $base = "http://127.0.0.1:$env:CAMPUSCLAW_PORT"
    Write-Output "Isolated Compose project: $project"
    Invoke-Compose build
    Invoke-Compose up -d --wait mysql
    Invoke-Compose run --rm --no-deps --entrypoint /app/migrate api up
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed -e SEED_TEACHER_A_PASSWORD -e SEED_STUDENT_A1_PASSWORD -e SEED_STUDENT_B1_PASSWORD api
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed-materials api
    Invoke-Compose up -d --wait api nginx
    $null = Request GET '/' $null 200
    $health = (Request GET '/health' $null 200).Content | ConvertFrom-Json
    if ($health.status -ne 'ok') { throw 'Health content mismatch' }
    # Health is liveness only. Wait separately for startup cleanup/business readiness.
    $ready = $false
    for ($i=0; $i -lt 30; $i++) {
        $response = Invoke-WebRequest "$base/api/me" -SkipHttpErrorCheck -TimeoutSec 10
        if ($response.StatusCode -eq 401) { $ready=$true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { throw 'Business readiness timed out' }
    $teacher = Login 'teacher_a' $env:SEED_TEACHER_A_PASSWORD
    $me = (Request GET '/api/me' $teacher 200).Content | ConvertFrom-Json
    if ($me.role -ne 'teacher') { throw 'Wrong identity' }
    $token = ((Request GET '/api/csrf' $teacher 200).Content | ConvertFrom-Json).csrf_token
    $inputPath = Join-Path $directory 'proxy-5MiB.txt'
    [IO.File]::WriteAllText($inputPath,('A' * (5 * 1024 * 1024)),[Text.UTF8Encoding]::new($false))
    $uploaded = Invoke-WebRequest "$base/api/materials" -Method POST -WebSession $teacher -Headers @{'X-CSRF-Token'=$token;Origin=$base} -Form @{file=(Get-Item -LiteralPath $inputPath)} -SkipHttpErrorCheck -TimeoutSec 60
    if ($uploaded.StatusCode -ne 201) { throw "5 MiB upload returned $($uploaded.StatusCode)" }
    $id = ($uploaded.Content | ConvertFrom-Json).material_id
    if (-not $id) { throw 'Missing material_id' }
    $list = (Request GET '/api/materials' $teacher 200).Content | ConvertFrom-Json
    if ($id -notin $list.items.id) { throw 'Uploaded material absent' }
    $detail = (Request GET "/api/materials/$id" $teacher 200).Content | ConvertFrom-Json
    if ($detail.text.Length -ne 5*1024*1024) { throw 'Extracted text truncated' }
    $downloadPath = Join-Path $directory 'download.txt'
    $download = Invoke-WebRequest "$base/api/materials/$id/file" -WebSession $teacher -OutFile $downloadPath -PassThru -SkipHttpErrorCheck
    if ($download.StatusCode -ne 200 -or $download.Headers.'Content-Disposition' -notmatch 'attachment') { throw 'Download response invalid' }
    if ((Get-FileHash $inputPath).Hash -ne (Get-FileHash $downloadPath).Hash) { throw 'Download bytes differ' }
    $null = Request GET '/api/missing' $teacher 404
    $null = Request POST '/api/logout' $teacher 200 '{}' $token
    $null = Request GET '/api/me' $teacher 401
    Write-Output 'PASS Nginx nine routes: HTML page, JSON API, login/me/logout, list/detail, exact 5 MiB upload/download hash'
    Invoke-Compose stop api
    $offline = (Request GET '/health' $null 503).Content | ConvertFrom-Json
    if ($offline.error -ne 'service_unavailable' -or -not $offline.message) { throw 'Proxy failure contract mismatch' }
    Write-Output 'PASS Nginx Go-unreachable health: 503 safe error/message JSON'
    Invoke-Compose up -d --wait api
    $null = Request GET '/health' $null 200
    Write-Output 'PASS T7.3 actual container proxy verification'
    if ($VerifyPersistence) {
        if ($PSVersionTable.PSVersion -lt [version]'7.4') { throw 'Binary backup requires PowerShell 7.4+' }
        $teacher = Login 'teacher_a' $env:SEED_TEACHER_A_PASSWORD
        Invoke-Compose stop nginx api
        # Native redirection preserves binary stdout in PowerShell 7.4+.
        $sqlPath = Join-Path $directory 'database.sql'
        $tarPath = Join-Path $directory 'materials.tar.gz'
        & $DockerCommand @composeArgs exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --user="$MYSQL_USER" --default-character-set=utf8mb4 --single-transaction --no-tablespaces --set-gtid-purged=OFF "$MYSQL_DATABASE"' > $sqlPath
        if ($LASTEXITCODE -ne 0) { throw 'Database backup failed' }
        & $DockerCommand @composeArgs run --rm -T --no-deps --entrypoint tar api -C /data -czf - uploads staging > $tarPath
        if ($LASTEXITCODE -ne 0) { throw 'File backup failed' }
        if ((Get-Item $sqlPath).Length -eq 0 -or (Get-Item $tarPath).Length -eq 0) { throw 'Empty backup' }
        Write-Output 'PASS consistent stopped-writer backup: MySQL dump and material archive both nonempty'
        Invoke-Compose down
        Invoke-Compose up -d --wait
        $null = Request GET '/api/me' $teacher 200
        $null = Request GET "/api/materials/$id" $teacher 200
        $null = Invoke-WebRequest "$base/api/materials/$id/file" -WebSession $teacher -OutFile $downloadPath
        if ((Get-FileHash $inputPath).Hash -ne (Get-FileHash $downloadPath).Hash) { throw 'Recreated file differs' }
        Write-Output 'PASS down/up preserves both volumes: original Cookie, material text and file hash survive'
        # Restore to a second unique project. Never overwrite the source database/volume.
        $sourceArgs = $composeArgs
        $sourceBase = $base
        $restoreProject = "$project-restore"
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
        $listener.Start(); $env:CAMPUSCLAW_PORT=[string]$listener.LocalEndpoint.Port; $listener.Stop()
        $base = "http://127.0.0.1:$env:CAMPUSCLAW_PORT"
        $composeArgs = @('compose','--env-file',$emptyEnv.FullName,'-f',(Join-Path $root 'compose.yaml'),'-p',$restoreProject)
        try {
            Invoke-Compose build
            Invoke-Compose up -d --wait mysql
            Invoke-Compose cp $sqlPath mysql:/tmp/campusclaw-restore.sql
            Invoke-Compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --user="$MYSQL_USER" --default-character-set=utf8mb4 "$MYSQL_DATABASE" < /tmp/campusclaw-restore.sql'
            Invoke-Compose run --rm -T --no-deps --volume "${directory}:/restore:ro" --entrypoint tar api -C /data -xzf /restore/materials.tar.gz
            Invoke-Compose up -d --wait api nginx
            $null = Request GET '/api/me' $teacher 200
            $restored = (Request GET "/api/materials/$id" $teacher 200).Content | ConvertFrom-Json
            if ($restored.text.Length -ne 5*1024*1024) { throw 'Restored knowledge text differs' }
            $null = Invoke-WebRequest "$base/api/materials/$id/file" -WebSession $teacher -OutFile $downloadPath
            if ((Get-FileHash $inputPath).Hash -ne (Get-FileHash $downloadPath).Hash) { throw 'Restored file differs' }
            Write-Output 'PASS restore to new project: original session, joined material/knowledge text and file SHA-256 match'
            Write-Output 'PASS T7.4 deployment, stop/recreate, backup and restore instructions'
        } finally {
            & $DockerCommand @composeArgs down
            $composeArgs = $sourceArgs; $base=$sourceBase
        }
    }
} finally {
    # Leave named volumes intact; this command affects only the unique test project.
    & $DockerCommand @composeArgs down
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
    Remove-Item -LiteralPath $emptyEnv.FullName
    Write-Output "Test project stopped; its named volumes retained: $project"
}
