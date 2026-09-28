param(
    [string]$DockerCommand = 'docker',
    [string]$DockerConfigDirectory = ''
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$variables = @('SESSION_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD','DB_USER','DB_NAME','CAMPUSCLAW_PORT','DOCKER_CONFIG')
$saved = @{}
$emptyEnvironment = New-TemporaryFile
try {
    foreach ($name in $variables) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    foreach ($name in @('SESSION_SECRET','DB_PASSWORD','MYSQL_ROOT_PASSWORD')) { [Environment]::SetEnvironmentVariable($name, [guid]::NewGuid().ToString('N'), 'Process') }
    $env:DB_USER = 'campusclaw'; $env:DB_NAME = 'campusclaw'; $env:CAMPUSCLAW_PORT = '8080'
    if ($DockerConfigDirectory) { $env:DOCKER_CONFIG = $DockerConfigDirectory }
    # Parse in memory. Never print rendered configuration: it contains credentials.
    $rendered = & $DockerCommand compose --env-file $emptyEnvironment.FullName -f (Join-Path $root 'compose.yaml') config --format json 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'Compose validation failed; rendered details suppressed' }
    $config = ($rendered -join "`n") | ConvertFrom-Json
    $services = @($config.services.PSObject.Properties.Name | Sort-Object)
    if (($services -join ',') -ne 'api,mysql,nginx') { throw 'Expected exactly three services' }
    if ($config.services.api.ports -or $config.services.mysql.ports) { throw 'Internal service exposes host ports' }
    $ports = @($config.services.nginx.ports)
    if ($ports.Count -ne 1 -or $ports[0].host_ip -ne '127.0.0.1' -or $ports[0].target -ne 80) { throw 'Nginx bind is not localhost-only' }
    if ($config.services.nginx.volumes) { throw 'Nginx must not mount material storage' }
    if ($config.services.mysql.volumes[0].source -ne 'mysql_data' -or $config.services.mysql.volumes[0].target -ne '/var/lib/mysql') { throw 'MySQL persistence mismatch' }
    if ($config.services.api.volumes[0].source -ne 'material_data' -or $config.services.api.volumes[0].target -ne '/data') { throw 'Material persistence mismatch' }
    if ($config.services.api.environment.MYSQL_ROOT_PASSWORD -or $config.services.nginx.environment) { throw 'Credentials leak across service boundaries' }
    if ($config.services.api.depends_on.mysql.condition -ne 'service_healthy') { throw 'MySQL readiness dependency missing' }
    if (($config.services.api.healthcheck.test -join ' ') -notmatch '/health' -or ($config.services.mysql.healthcheck.test -join ' ') -notmatch 'SELECT 1') { throw 'Health semantics mismatch' }
    foreach ($service in @('api','nginx')) { if ($config.services.$service.build.args) { throw 'Build arguments may not carry credentials' } }
    Write-Output 'PASS docker compose config: api/mysql/nginx; only 127.0.0.1:8080 published'
    Write-Output 'PASS mysql_data -> /var/lib/mysql; material_data -> /data; Nginx has no data mounts'
    Write-Output 'PASS independent database readiness and HTTP liveness; no build secrets; no root secret in API'
} finally {
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
    Remove-Item -LiteralPath $emptyEnvironment.FullName
}
