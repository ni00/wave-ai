#!/usr/bin/env python3
"""Real PostgreSQL/S3 + deterministic model. Every resource is owned and removed."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]

def run(*args, **kwargs):
    return subprocess.run(args, check=True, cwd=ROOT, **kwargs)

def output(*args): return run(*args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True).stdout.strip()

def ready(check, seconds=60):
    until=time.monotonic()+seconds
    while time.monotonic()<until:
        try:
            if check(): return
        except subprocess.CalledProcessError: pass
        time.sleep(.25)
    raise RuntimeError('test service readiness timed out')

def main():
    suffix=secrets.token_hex(5);pg=f'wave-clients-pg-{suffix}';s3=f'wave-clients-s3-{suffix}'
    processes=[]
    with tempfile.TemporaryDirectory(prefix='wave-client-integration-') as directory:
        work=Path(directory)
        try:
            output('docker','run','--name',pg,'-e','POSTGRES_USER=wave','-e','POSTGRES_PASSWORD=wave-test','-e','POSTGRES_DB=wave_test','-p','127.0.0.1::5432','-d','postgres:16-alpine')
            ready(lambda: bool(output('docker','exec',pg,'pg_isready','-U','wave')))
            pgport=output('docker','port',pg,'5432/tcp').rsplit(':',1)[1]
            output('docker','run','--name',s3,'-e','AWS_ACCESS_KEY_ID=wave-test','-e','AWS_SECRET_ACCESS_KEY=wave-test-password','-e','S3_BUCKET=wave','-p','127.0.0.1::8333','-d','chrislusf/seaweedfs:4.46','mini','-dir=/data','-ip=127.0.0.1','-master.telemetry=false','-admin.ui=false','-webdav=false','-s3.port.iceberg=0','-s3.port.lance=0','-s3.autoCreateBucket=false')
            endpoint='http://127.0.0.1:'+output('docker','port',s3,'8333/tcp').rsplit(':',1)[1]
            ready(lambda: run('curl','-fsS','--head','--max-time','1','--aws-sigv4','aws:amz:us-east-1:s3','--user','wave-test:wave-test-password',endpoint+'/wave',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0)
            env=dict(os.environ,WAVE_TEST_DATABASE_URL=f'postgres://wave:wave-test@127.0.0.1:{pgport}/wave_test?sslmode=disable',WAVE_DATA_DIR=str(work/'data'),WAVE_STORAGE_BACKEND='s3',WAVE_S3_ENDPOINT=endpoint,WAVE_S3_REGION='us-east-1',WAVE_S3_BUCKET='wave',WAVE_S3_PATH_STYLE='true',WAVE_S3_ALLOW_HTTP='true',WAVE_MASTER_KEY=secrets.token_hex(32),AWS_ACCESS_KEY_ID='wave-test',AWS_SECRET_ACCESS_KEY='wave-test-password',WAVE_SANDBOX_BACKEND='local',WAVE_SANDBOX_ALLOW_LOCAL='true',WAVE_SANDBOX_LOCAL_ROOT=str(work/'sandboxes'))
            binary=work/'service';run('go','build','-o',str(binary),'./tests/clients/service')
            cli=work/'wavectl';run('go','build','-C','cli','-o',str(cli),'.')
            connection=work/'connection.json'
            with (work/'service.log').open('w') as log:
                service=subprocess.Popen([str(binary),str(connection)],cwd=ROOT,env=env,stdout=log,stderr=log);processes.append(service)
                def started():
                    if service.poll() is not None: raise RuntimeError((work/'service.log').read_text())
                    return connection.exists()
                ready(started)
                credentials=json.loads(connection.read_text());env.update(WAVE_BASE_URL=credentials['url'],WAVE_API_KEY=credentials['key'],WAVE_CLIENT_E2E='1',WAVE_TEST_CLI=str(cli),WAVE_CONFIG_FILE=str(work/'cli-config.json'))
                run('go','test','-C','sdks/go','-run','TestRealService','-count=1','.',env=env)
                run('uv','run','--project','sdks/python','--frozen','python','tests/clients/e2e.py',env=env)
                run('node','tests/clients/e2e.mjs',env=env)
                print('Real-service Go/Python/TS/CLI task approval, result, SSE and S3 file tests passed.')
        finally:
            for process in processes:
                process.terminate()
                try: process.wait(timeout=10)
                except subprocess.TimeoutExpired: process.kill();process.wait()
            subprocess.run(['docker','rm','-fv',pg,s3],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)

if __name__=='__main__': main()
