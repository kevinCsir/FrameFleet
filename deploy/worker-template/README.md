# Worker Runtime Template

This directory contains a small generator for creating one external WorkerGo
runtime instance. The generated runtime keeps environment, data, input, logs,
and lifecycle scripts together so multiple workers can run on one machine.

## Create A Worker

From the repository root:

```bash
deploy/worker-template/init-worker.sh \
  --name worker-19001 \
  --root /home/ckw/framefleet-workers \
  --port 19001 \
  --entry http://100.76.141.122:19080 \
  --advertised 100.99.5.45:19001 \
  --slots 2
```

This creates:

```text
/home/ckw/framefleet-workers/worker-19001/
  worker.env
  run.sh
  stop.sh
  status.sh
  logs.sh
  input/
  logs/
  data/spool/{uploads,outgoing,artifacts,results,tmp}/
```

Put source `.mp4` files into `input/`. WorkerGo scans that directory and
registers jobs with Entry.

## Operate A Worker

```bash
/home/ckw/framefleet-workers/worker-19001/run.sh
/home/ckw/framefleet-workers/worker-19001/status.sh
/home/ckw/framefleet-workers/worker-19001/logs.sh
/home/ckw/framefleet-workers/worker-19001/logs.sh -f
/home/ckw/framefleet-workers/worker-19001/stop.sh
```

`run.sh` cleans intermediate spool directories before starting:

```text
data/spool/uploads
data/spool/outgoing
data/spool/artifacts
data/spool/tmp
```

It does not clean `input/` or `data/spool/results/`. `stop.sh` terminates the
recorded process group so WorkerGo, C++ engine slots, and ffmpeg children are
stopped together.

WorkerGo logs go to:

```text
logs/worker-agent.log
```

The launcher wrapper writes only its own startup failures to:

```text
logs/launcher.log
```

## Useful Options

```text
--name NAME                  Worker directory name, for example worker-19001.
--root DIR                   Parent directory for generated workers.
--port PORT                  Local listen port; sets WORKER_LISTEN_ADDR.
--entry URL                  Entry base URL including http://.
--advertised HOST:PORT       Address registered with Entry.
--slots N                    Worker slot count.
--engine PATH                C++ engine binary path.
--repo DIR                   FrameFleet repository path.
--force                      Overwrite an existing worker directory.
```

Run `deploy/worker-template/init-worker.sh --help` for the full list.
