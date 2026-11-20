# Set up vSR's decision runtime

vSR asks a small decision model about every child task, prompt, read, shell command and answer. This skill gets that model answering on this machine, or points vSR at one that already answers, picks which apps run and their thresholds, writes them, and checks each app, as the Claude Code plugin's `/vsr:setup` does. Run each command yourself, show what it prints, and ask the user only what they decide, with OpenCode's question tool. Nothing here needs sudo; if a step seems to, stop and tell the user why.

vSR gives you two tools for this: `vsr_settings` says vSR's settings with no input and sets what the user chose with input, kept over the plugin options as `/vsr-config` keeps them; `vsr_check` asks one case per enabled app, as the panel's runtime line does.

The runtime is the same one the Claude Code plugin installs: `~/.local/share/vsr/venv`, port 8790, so one runtime serves every agent on this machine.

## Steps

1. **Look first.** Run these and summarize them in a few lines; they install nothing:

   - `curl -s --max-time 3 http://127.0.0.1:8790/health` — a runtime already runs here when this prints `"status":"ready"`; `"loading"` means one is still starting.
   - `nvidia-smi --query-gpu=name,memory.total --format=csv,noheader` (or `rocm-smi --showproductname` on AMD) — a GPU, when there is one.
   - `python3 --version`, `df -h ~` and `free -h` (`sysctl hw.memsize` on macOS).
   - `vsr_settings` with no input: the apps, waits, router goal and turns, thresholds and runtime vSR runs with now.

   Done when you know whether a runtime answers, what this machine has, and vSR's settings.

2. **Recommended or custom.** Ask one question:

   - **Recommended setup (recommended)**: say what it does on this machine from step 1: keep the runtime that answers, or install and run vela3-lite on this machine's GPU, or its CPU when there is none. Every app at its default (check stays off; turn it on later with a GPU or remote runtime) and the default thresholds.
   - **Custom**: the user picks where the model runs, which apps run, and the thresholds.

   On Recommended, keep a runtime that answers (skip to step 6) or install on the GPU or CPU step 1 found (step 4), then go to step 9 with nothing to change. On Custom, go to step 3. Done when the user picked one.

3. **Where the model runs.** Custom only. Ask the user one question, the first option marked recommended by what step 1 found:

   - **This machine's GPU**: fastest; vela3-lite uses about 2 GB of GPU memory.
   - **This machine's CPU**: about 2.3 GB of RAM and 0.1 to 0.25 s per question on an x86 CPU with bf16, up to about 2 s on a laptop or Apple Silicon CPU; at that speed apps often step aside within their wait.
   - **Keep the running one**, only when a runtime answers already: nothing is installed. Skip to step 6.
   - **A runtime on another machine**: nothing is installed. Tell the user plainly that everything the enabled apps check is sent to that host: each read and shell output, fetched page, prompt, child task, shell command and answer. Skip to step 6.

   Done when the user picked one. An Apple GPU cannot run vela3 yet, so a Mac runs it on the CPU.

4. **Install**, only when the user agrees to the download: about 200 MB of CPU torch, or 2 to 3 GB of CUDA or ROCm torch, plus about 1.8 GB of model weights on first start. Use a Python 3.10 or later; skip any command whose result already exists (`~/.local/share/vsr/venv/bin/vllm-srun` means it is installed). Each takes minutes, so run the pip commands in the background and wait for them.

   First check the disk, as the Claude Code plugin's setup does: the install needs the venv (1.1 GiB for a CPU torch, 6 GiB for a GPU one; none when `vllm-srun` is installed), the model (1.8 GiB; none when `~/.cache/huggingface/hub/models--vllm-sr--vela3-lite` exists, or under `$HF_HOME/hub` when that is set), and 1 GiB to spare. Compare that with the smaller free space of `df -BG ~/.local/share` and of the Hugging Face cache's disk. When it is short, say `not installing: only N GiB disk free, M needed` and stop; go on only when the user says the space is there (the model on another disk, say).

   ```bash
   python3 -m venv ~/.local/share/vsr/venv
   ~/.local/share/vsr/venv/bin/python -m pip install --upgrade pip
   # CPU on Linux only: the CPU wheels save several GB of CUDA libraries.
   ~/.local/share/vsr/venv/bin/python -m pip install --upgrade 'torch>=2.4' --index-url https://download.pytorch.org/whl/cpu
   # GPU, or a Mac: torch from PyPI instead.
   ~/.local/share/vsr/venv/bin/python -m pip install --upgrade 'torch>=2.4'
   # The runtime, pinned to the commit vSR was tested against; drop `-c <lock>` on a Python older than 3.12.
   ~/.local/share/vsr/venv/bin/python -m pip install --upgrade -c https://raw.githubusercontent.com/vllm-project/semantic-router/579e0ca2a0b1b03580eada4213da84fe68db57c2/src/model-runtime/requirements-lock.txt 'vllm-srun @ git+https://github.com/vllm-project/semantic-router.git@579e0ca2a0b1b03580eada4213da84fe68db57c2#subdirectory=src/model-runtime'
   ```

   On failure, show the last pip lines and stop. For ROCm, torch from PyPI is a CUDA build: point the user to pytorch.org for the ROCm wheel index. Done when `~/.local/share/vsr/venv/bin/vllm-srun --help` prints its usage.

5. **Start it.** First the port: when `curl -s --max-time 3 http://127.0.0.1:8790/health` prints `"status":"ready"`, a runtime already runs there, so keep it and skip to step 6; when something else holds port 8790 (`curl` connects but gets no vSR health, or `ss -ltn 'sport = :8790'` lists it), it may be a runtime still loading: say so and wait (step 6) rather than start a second one.

   On a CPU, time bf16 against float32 with the venv's own torch, as the Claude Code plugin's setup does, and show the `dtype` it picks:

   ```bash
   ~/.local/share/vsr/venv/bin/python - <<'PY'
   import json, time, torch
   def ms(dtype, n=1024, tokens=256, reps=20):
       w = torch.randn(n * 4, n, dtype=dtype)
       x = torch.randn(tokens, n, dtype=dtype)
       with torch.inference_mode():
           for _ in range(3):
               x @ w.T
           start = time.perf_counter()
           for _ in range(reps):
               x @ w.T
           return (time.perf_counter() - start) / reps * 1000
   f32, bf16 = ms(torch.float32), ms(torch.bfloat16)
   print(json.dumps({"float32": f32, "bfloat16": bf16, "dtype": "float32" if bf16 > 1.5 * f32 else "auto"}))
   PY
   ```

   When it says `float32` (a CPU without native bf16, where bf16 is several times slower), add `--dtype float32` to the command below; otherwise leave the runtime's own `auto`. On a GPU, skip this.

   Ask whether it should run for this login only (it stops at logout or reboot) or keep running across reboots. For this login, with `cpu`, `cuda` or `rocm` as the device, and its pid kept where the README's uninstall steps stop it:

   ```bash
   mkdir -p ~/.local/share/vsr
   nohup ~/.local/share/vsr/venv/bin/vllm-srun serve vllm-sr/vela3-lite --device <device> --host 127.0.0.1 --port 8790 [--dtype float32] >> ~/.local/share/vsr/runtime.log 2>&1 &
   echo $! > ~/.local/share/vsr/runtime.pid
   ```

   Across reboots on Linux, write that command as the `ExecStart` of a systemd user unit at `~/.config/systemd/user/vsr-runtime.service` (with `Restart=on-failure` and the log appended to `~/.local/share/vsr/runtime.log`), show it to the user, and only on their yes run `systemctl --user daemon-reload && systemctl --user enable --now vsr-runtime`. On macOS, the same as a launchd agent at `~/Library/LaunchAgents/local.vsr-runtime.plist` (label `local.vsr-runtime`, `RunAtLoad` and `KeepAlive`), loaded with `launchctl bootstrap "gui/$(id -u)"` on the user's yes. These are the names the Claude Code plugin's setup uses, so either plugin finds the service the other wrote. Done when the process runs.

6. **Wait until it answers.** The first start downloads the model, which can take several minutes. Poll `curl -s --max-time 3 http://127.0.0.1:8790/health` every 10 seconds, up to 15 minutes, until it prints `"status":"ready"`. For a remote runtime, use the user's URL with `/v1/decisions` replaced by `/health`; a Router listener answers `"status":"ok"` there and a hosted API has no `/health`, so for those `/v1/models` listing a model means it answers. If it never answers, show the last lines of `~/.local/share/vsr/runtime.log`. Done when it is ready.

   For a runtime on another machine, tell the user to run `/connect`, pick **vSR decision runtime**, then **Runtime with a token** (or **without a token**) with its URL; OpenCode keeps the token in its own credential store, so never ask the user to paste it into the chat, and leave the model empty unless the runtime serves several. Wait until they say it is done. A runtime on this machine needs no `/connect`: vSR asks `http://127.0.0.1:8790/v1/decisions` by default.

7. **Apps.** Custom only. Ask one question, multiple choice, one line per app, the ones on by default marked recommended:

   - **router** (picks the model and effort of each new subagent from your three tiers by its task's difficulty), **escalate** (after you push back, the agent re-checks and the next subagent goes one tier up): both need `routing.tiers`, three models from one provider, in the plugin options in `opencode.jsonc`; when there are none, say so and offer to add them there.
   - **redact** (hides personal data and secrets in read and shell output), **shield** (warns the agent when a file or page tries to give it instructions), **guard** (asks you before risky shell commands).
   - **check** (marks an answer that claims more than the agent read; off by default, slow on a CPU runtime).

   Add to the question: each question costs about 0.1 to 0.2 s on an x86 CPU, less on a GPU, and redact and shield run on every read. Done when every app is on or off.

8. **Thresholds.** Custom only. Ask one question: **Defaults (recommended)**, measured on vela3-lite, or **Advanced**. For Advanced, show the threshold lines step 1's `vsr_settings` listed for the enabled apps, each with its value and what it does, and let the user name the values to change. Done when you have the list of changed thresholds, possibly empty.

9. **Write.** Call `vsr_settings` once with what steps 7 and 8 chose: `apps` with each app on or off, and `thresholds` with each app whose lines changed (all of that app's lines, in the order `vsr_settings` lists them). On Recommended, call it with nothing to change. Show what it says. Done when it shows the settings the user chose.

10. **Check.** Call `vsr_check` and show each line. An app that has no answer, or decided otherwise than on vela3-lite, is said plainly with what to do: a runtime that does not answer (step 6), a slow one (a shorter wait in `/vsr-config`, or a GPU or remote runtime), a model that answers no score questions (the router steps aside). Done when every enabled app answered, or the user knows why one did not.

11. **Hand over.** Tell the user, in this order:

- `/vsr` opens the decisions panel; its top turns apps on and off and picks their waits.
- `/vsr-config` changes any setting later, thresholds included, and its last row checks the runtime again; so does a click on the panel's runtime line.
- After some use, `/vsr-calibrate` fits the thresholds to their own answers.

Done when the user has these steps.
