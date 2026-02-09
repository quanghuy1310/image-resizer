Image Resize Tool

Overview
- Bulk image resizing tool using libvips via `bimg`.
- Supports multi-process (parent spawns children per folder) and single-process modes.

Quick start
1. Edit `.env` to set `BASE_DIR`, `PROCESS_MODE`, and other options.
2. Build using `build.ps1` on Windows (sets CGO env for libvips):

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1
```

3. Run:

```powershell
# Parent mode (multi-process)
.\Resize_tool.exe

# Child mode (used internally)
.\Resize_tool.exe --child --folder="D:\path\to\folder"
```

Important environment variables (added/updated)
- `IMAGE_PREFIXES` - comma-separated, case-insensitive prefixes to select files for resizing. Default: `full_,vehicle_`. Leave empty to process all JPEG files.
- `LOG_FILE`, `RESIZE_HISTORY_LOG`, `LOG_MAX_MB` - logging and rotation.
- `PROCESSES`, `THREADS_PER_PROCESS`, `BATCH_SIZE` - concurrency tuning.
- `RESIZE_WIDTH`, `RESIZE_HEIGHT`, `QUALITY` - image parameters.

Graceful shutdown and process control
- Parent process installs signal handler (Ctrl+C) and cancels internal context.
- Parent creates a Windows Job Object (when available) and assigns child processes to it so that if the parent dies unexpectedly, the OS terminates child processes as well.
- Child processes also watch the parent PID and will exit if they detect the parent process has died.

Notes for maintainers
- Logging uses an atomic writer wrapper so rotation can swap files without races.
- To change which files are targeted for resize, edit `IMAGE_PREFIXES` in `.env`.

Service examples

systemd (Linux)

Create `/etc/systemd/system/image-resize.service`:

```
[Unit]
Description=Image Resize Tool
After=network.target

[Service]
Type=simple
User=resizeuser
WorkingDirectory=/opt/image-resize
ExecStart=/opt/image-resize/Resize_tool --config /opt/image-resize/.env
Restart=on-failure
KillMode=control-group

[Install]
WantedBy=multi-user.target
```

Windows (nssm example)

Using `nssm` (Non-Sucking Service Manager) create a service that runs the exe. Example command:

``powershell
nssm install ImageResize "C:\path\to\Resize_tool.exe"
# set AppDirectory and AppParameters as needed
``

Behavior on shutdown and Ctrl+C

- If you stop the parent with Ctrl+C, the parent cancels its context and waits for children to exit gracefully (they receive a context cancel via `exec.CommandContext`).
- On Windows we also create a Job Object and assign children to it. If the parent process is killed or the console is closed unexpectedly, the OS will terminate all child processes assigned to the job.
- On non-Windows platforms, each child monitors the parent PID and will exit if it detects the parent has died. This combination makes shutdown safe in normal and abrupt parent exits.

Dry-run mode

- Set `DRY_RUN=true` in `.env` to simulate work. In dry-run mode the tool will not write files or spawn children; it only logs actions and records entries to the history log with `[DRYRUN]`.

Support
- Open an issue or ask for unit tests / service snippets if you want them added.
