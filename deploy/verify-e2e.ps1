#Requires -Version 7.5
param(
    [string]$DockerCommand = 'docker',
    [string]$DockerConfigDirectory = ''
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$project = 'campusclaw-t8-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$names = @('DOCKER_CONFIG','SESSION_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD','DB_NAME','DB_USER','CAMPUSCLAW_PORT','SEED_TEACHER_A_PASSWORD','SEED_STUDENT_A1_PASSWORD','SEED_STUDENT_B1_PASSWORD')
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
    $result = (Request POST '/api/login' $session 200 $body $token.csrf_token).Content | ConvertFrom-Json
    Fields $result @('user')
    Fields $result.user @('id','username','role','class_id')
    if ($result.user.username -ne $Username) { throw 'Login identity mismatch' }
    return $session
}
function Fields($Object, [string[]]$Expected) {
    if ((($Object.PSObject.Properties.Name | Sort-Object) -join ',') -cne (($Expected | Sort-Object) -join ',')) { throw 'Response field contract mismatch' }
}
function JSON([string]$Path, $Session) {
    # Preserve RFC3339 wire strings; PowerShell 7.5 otherwise turns them into DateTime.
    return ((Request GET $Path $Session 200).Content | ConvertFrom-Json -DateKind String)
}
function ErrorResponse($Response, [string]$Code, [string]$Message) {
    $obj = $Response.Content | ConvertFrom-Json
    Fields $obj @('error','message')
    if ($obj.error -cne $Code -or $obj.message -cne $Message) { throw 'Safe error contract mismatch' }
}
function SQL([string]$Statement) {
    $result = $Statement | & $DockerCommand @composeArgs exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names'
    if ($LASTEXITCODE -ne 0) { throw 'Isolated SQL verification failed' }
    return ($result -join "`n")
}
function CookieCopy($Session) {
    $copy = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $value = $Session.Cookies.GetCookies([uri]$base)['campusclaw_session'].Value
    $copy.Cookies.Add([uri]$base,[Net.Cookie]::new('campusclaw_session',$value,'/'))
    return $copy
}
function Upload($Session, [string]$Path, [int]$Status, [string]$Token, [string]$ClassID) {
    $args = @{Uri="$base/api/materials?class_id=$ClassID&role=teacher";Method='POST';SkipHttpErrorCheck=$true;TimeoutSec=30;Form=@{file=(Get-Item -LiteralPath $Path);class_id=$ClassID;role='teacher'};Headers=@{Origin=$base;'X-CSRF-Token'=$Token}}
    if ($Session) { $args.WebSession=$Session }
    $response=Invoke-WebRequest @args
    if ($response.StatusCode -ne $Status -or $response.Headers.'Content-Type' -notmatch 'application/json') { throw "Upload expected $Status, received $($response.StatusCode)" }
    return $response
}
function Snapshot {
    return SQL 'SELECT id,name FROM classes ORDER BY id; SELECT id,username,role,class_id,SHA2(password_hash,256) FROM users ORDER BY id; SELECT id,class_id,uploader_id,source,original_name,storage_key,size,created_at FROM materials ORDER BY id; SELECT id,material_id,class_id,SHA2(extracted_text,256) FROM knowledge_entries ORDER BY id;'
}
function FilesSnapshot {
    $output = & $DockerCommand @composeArgs exec -T api sh -c 'find /data/uploads /data/staging -type f -exec sha256sum {} \; | sort'
    if ($LASTEXITCODE -ne 0) { throw 'File snapshot failed' }
    return ($output -join "`n")
}
function Matrix($Actors) {
    foreach ($entry in $Actors.GetEnumerator()) {
        $isB = $entry.Key -eq 'student_b1'
        $class = if ($isB) { $classB } else { $classA }
        $other = if ($isB) { $classA } else { $classB }
        $list = JSON "/api/materials?class_id=$other&role=teacher" $entry.Value
        Fields $list @('items')
        $expectedIds = if ($isB) { @($bID) } else { @($aIDs) }
        if ((($list.items.id | Sort-Object) -join ',') -ne (($expectedIds | Sort-Object) -join ',')) { throw 'Class list mismatch' }
        foreach ($item in $list.items) {
            Fields $item @('id','title','class_id','created_at')
            if ($item.class_id -ne $class -or $item.created_at -notmatch '^\d{4}-\d{2}-\d{2}T.*Z$') { throw 'Metadata mismatch' }
            $detail = JSON "/api/materials/$($item.id)" $entry.Value
            Fields $detail @('id','title','class_id','created_at','text')
            if ($detail.text -cne [IO.File]::ReadAllText($fileByID[[string]$item.id])) { throw 'Extracted fixture text mismatch' }
            $downloadPath=Join-Path $directory 'download.bin'
            $response=Invoke-WebRequest "$base/api/materials/$($item.id)/file" -WebSession $entry.Value -OutFile $downloadPath -PassThru
            if ($response.Headers.'Content-Type' -notmatch 'application/octet-stream' -or $response.Headers.'Content-Disposition' -notmatch 'attachment') { throw 'Attachment contract mismatch' }
            if ((Get-FileHash $downloadPath).Hash -ne (Get-FileHash $fileByID[[string]$item.id]).Hash) { throw 'Attachment hash mismatch' }
        }
        $foreignIds = if ($isB) { @($aIDs) } else { @($bID) }
        foreach ($id in (@($foreignIds) + @(999999))) {
            foreach ($suffix in @('','/file')) {
                ErrorResponse (Request GET "/api/materials/$id$suffix" $entry.Value 404) 'not_found' '资源不存在'
            }
        }
    }
    Write-Output 'PASS three-account matrix: exact own-class IDs/text/file hashes, forged filters ignored, both-way direct IDs 404'
}
try {
    foreach ($name in $names) { $saved[$name]=[Environment]::GetEnvironmentVariable($name,'Process') }
    if ($DockerConfigDirectory) { $env:DOCKER_CONFIG=$DockerConfigDirectory }
    foreach ($name in @('SESSION_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD','SEED_TEACHER_A_PASSWORD','SEED_STUDENT_A1_PASSWORD','SEED_STUDENT_B1_PASSWORD')) { [Environment]::SetEnvironmentVariable($name,[guid]::NewGuid().ToString('N'),'Process') }
    $env:DB_NAME='campusclaw'; $env:DB_USER='campusclaw'
    $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
    $listener.Start(); $env:CAMPUSCLAW_PORT=[string]$listener.LocalEndpoint.Port; $listener.Stop()
    $base="http://127.0.0.1:$env:CAMPUSCLAW_PORT"
    Write-Output "Isolated Compose project: $project"
    Invoke-Compose build
    Invoke-Compose up -d --wait mysql
    Invoke-Compose run --rm --no-deps --entrypoint /app/migrate api up
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed -e SEED_TEACHER_A_PASSWORD -e SEED_STUDENT_A1_PASSWORD -e SEED_STUDENT_B1_PASSWORD api
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed-materials api
    Invoke-Compose up -d --wait api nginx
    $health=JSON '/health' $null; Fields $health @('status')
    if ($health.status -ne 'ok') { throw 'Health mismatch' }
    $actors=@{}
    $actors.teacher_a=Login 'teacher_a' $env:SEED_TEACHER_A_PASSWORD
    $actors.student_a1=Login 'student_a1' $env:SEED_STUDENT_A1_PASSWORD
    $actors.student_b1=Login 'student_b1' $env:SEED_STUDENT_B1_PASSWORD
    foreach ($name in $actors.Keys) {
        $me=JSON '/api/me' $actors[$name]; Fields $me @('id','username','role','class_id')
        if ($me.username -ne $name -or $me.role -ne $(if ($name -eq 'teacher_a') {'teacher'} else {'student'})) { throw 'Identity mismatch' }
    }
    $classA=(JSON '/api/me' $actors.teacher_a).class_id
    $classB=(JSON '/api/me' $actors.student_b1).class_id
    $empty=JSON '/api/materials' $actors.teacher_a; Fields $empty @('items')
    if ($empty.items -isnot [array] -or $empty.items.Count -ne 0) { throw 'Expected empty items array' }
    $bID=(JSON '/api/materials' $actors.student_b1).items[0].id
    $fileByID=@{[string]$bID=(Join-Path $root 'backend/fixtures/class-b-seed.txt')}
    $aIDs=@()
    foreach ($filename in @('class-a-intro.txt','class-a-notes.md')) {
        $source=Join-Path $root "backend/fixtures/$filename"
        $token=JSON '/api/csrf' $actors.teacher_a; Fields $token @('csrf_token')
        $result=(Upload $actors.teacher_a $source 201 $token.csrf_token $classB).Content | ConvertFrom-Json
        Fields $result @('material_id')
        $aIDs += $result.material_id
        $fileByID[[string]$result.material_id]=$source
    }
    Matrix $actors
    $before=Snapshot; $filesBefore=FilesSnapshot
    foreach ($name in @('student_a1','student_b1')) {
        $token=(JSON '/api/csrf' $actors[$name]).csrf_token
        ErrorResponse (Upload $actors[$name] $source 403 $token $classA) 'forbidden' '无权执行此操作'
    }
    ErrorResponse (Upload $null $source 401 'invalid' $classA) 'unauthenticated' '请先登录'
    ErrorResponse (Upload $actors.teacher_a $source 403 'invalid' $classA) 'csrf_invalid' '请求校验失败'
    foreach ($session in @($null,(CookieCopy $actors.teacher_a))) {
        if ($session) { $session.Cookies.GetCookies([uri]$base)['campusclaw_session'].Value='forged' }
        foreach ($path in @('/api/materials',"/api/materials/$($aIDs[0])","/api/materials/$($aIDs[0])/file")) { ErrorResponse (Request GET $path $session 401) 'unauthenticated' '请先登录' }
    }
    if ((Snapshot) -cne $before -or (FilesSnapshot) -cne $filesBefore) { throw 'Rejected request changed data or files' }
    $replay=CookieCopy $actors.student_a1
    $token=(JSON '/api/csrf' $actors.student_a1).csrf_token
    $logout=(Request POST '/api/logout' $actors.student_a1 200 '{}' $token).Content | ConvertFrom-Json
    Fields $logout @('ok'); if ($logout.ok -ne $true) { throw 'Logout contract mismatch' }
    ErrorResponse (Request GET '/api/materials' $replay 401) 'unauthenticated' '请先登录'
    $actors.student_a1=Login 'student_a1' $env:SEED_STUDENT_A1_PASSWORD
    Write-Output 'PASS T8.1 nine-route fields/status/error messages, anonymous/forged/student/CSRF rejection without side effects and logout replay'

    $expired=Login 'student_a1' $env:SEED_STUDENT_A1_PASSWORD
    $raw=$expired.Cookies.GetCookies([uri]$base)['campusclaw_session'].Value.Replace('-','+').Replace('_','/')
    $raw=$raw.PadRight([int]([Math]::Ceiling($raw.Length/4)*4),'=')
    $hmac=[Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($env:SESSION_SECRET))
    try { $hash=[Convert]::ToHexString($hmac.ComputeHash([Convert]::FromBase64String($raw))) } finally { $hmac.Dispose() }
    $changed=SQL "SET @sid=UNHEX('$hash'); PREPARE expire_one FROM 'UPDATE sessions SET created_at=UTC_TIMESTAMP(6)-INTERVAL 9 HOUR,expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,csrf_token_hash=NULL,csrf_expires_at=NULL WHERE session_id_hash=?'; EXECUTE expire_one USING @sid; SELECT ROW_COUNT(); DEALLOCATE PREPARE expire_one;"
    if ($changed -ne '1') { throw 'Expected exactly one expired fixture session' }
    ErrorResponse (Request GET '/api/materials' $expired 401) 'unauthenticated' '请先登录'
    $before=Snapshot; $filesBefore=FilesSnapshot
    Invoke-Compose down
    Invoke-Compose up -d --wait
    Matrix $actors
    foreach ($path in @('/api/materials',"/api/materials/$($aIDs[0])","/api/materials/$($aIDs[0])/file")) { ErrorResponse (Request GET $path $expired 401) 'unauthenticated' '请先登录' }
    if ((Snapshot) -cne $before -or (FilesSnapshot) -cne $filesBefore) { throw 'Recreation changed persisted rows or files' }
    Write-Output 'PASS T8.2 original three Cookies before re-login; expired Cookie remains 401; both volumes, rows and hashes unchanged'
    Invoke-Compose stop nginx api
    # Different valid seed passwords prove rerun does not reset stored account hashes.
    $oldTeacher=$env:SEED_TEACHER_A_PASSWORD
    $env:SEED_TEACHER_A_PASSWORD=[guid]::NewGuid().ToString('N')
    $oldA1=$env:SEED_STUDENT_A1_PASSWORD; $oldB1=$env:SEED_STUDENT_B1_PASSWORD
    Invoke-Compose run --rm --no-deps --entrypoint /app/migrate api up
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed -e SEED_TEACHER_A_PASSWORD -e SEED_STUDENT_A1_PASSWORD -e SEED_STUDENT_B1_PASSWORD api
    Invoke-Compose run --rm --no-deps --entrypoint /app/seed-materials api
    if ((Snapshot) -cne $before) { throw 'Repeated initialization changed rows or hashes' }
    Invoke-Compose up -d --wait api nginx
    if ((FilesSnapshot) -cne $filesBefore) { throw 'Repeated initialization changed files' }
    $actors.teacher_a=Login 'teacher_a' $oldTeacher
    $actors.student_a1=Login 'student_a1' $oldA1
    $actors.student_b1=Login 'student_b1' $oldB1
    Matrix $actors
    Write-Output 'PASS T8.2 serial migrate/seed rerun: no change, passwords/IDs/text/files preserved, fresh login and both-way isolation'
    $logs=(& $DockerCommand @composeArgs logs --no-color 2>&1) -join "`n"
    foreach ($secret in @($env:SESSION_SECRET,$env:DB_PASSWORD,$env:MYSQL_ROOT_PASSWORD,$oldTeacher,$oldA1,$oldB1)) { if ($logs.Contains($secret)) { throw 'Credential found in logs; details suppressed' } }
    foreach ($session in $actors.Values) { if ($logs.Contains($session.Cookies.GetCookies([uri]$base)['campusclaw_session'].Value)) { throw 'Session ID found in logs; details suppressed' } }
    if ((SQL "SELECT COUNT(*) FROM users WHERE password_hash NOT LIKE '`$scrypt`$%';") -ne '0') { throw 'Password storage format mismatch' }
    Write-Output 'PASS credential check: password hashes only, supplied secrets absent from service logs'
    Write-Output 'PASS all T8.1/T8.2 container checks'
} finally {
    & $DockerCommand @composeArgs down
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
    Remove-Item -LiteralPath $emptyEnv.FullName
    Write-Output "Test containers stopped; volumes retained: $project"
}
