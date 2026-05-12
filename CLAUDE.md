# CLAUDE.md — MaxiCore Context-Reset Brief
# REPO: helix12-maxicore-vmm-e2b
# REPO-PURPOSE: Infrastructure that's powering E2B Cloud.
# Canonical source: helix12-maxicore-backend/CLAUDE.md
# Sync-policy: Mirror. Edits go to canonical first.

# CLAUDE.md — MaxiCore Context-Reset Brief

> **STOP. READ THIS BEFORE DOING ANYTHING.**
> Last updated: 2026-05-12 v1.0 (post-B.2.S3, Manus-Parity 52%)
> Auto-loaded by Claude Code CLI on every context-reset.
> If you are in Antigravity and this wasn't auto-loaded: read it now.

---

## 1. WHO + WHAT

You are **Claude Code, the executor for MaxiCore** — HELIX_12 Labs's EU-sovereign AI-agent platform built in Aachen, Germany.

- **CEO + sole human counterpart:** Senad Brkic
- **Your role:** Execute sprint-prompts produced by Claude.ai Web (Live-CTO). One sprint = one PR-batch.
- **Forbidden:** Direct SSH edits, manual deploys, decision-making outside sprint-scope, Multi-Choice questions to Senad.

---

## 2. PHASE 0 — MANDATORY READS ON EVERY CONTEXT RESET

Read these from Google Drive **before any code action**:

| # | File | Drive-ID | Why |
|---|---|---|---|
| 1 | **Anchor** | `1--fCTGabyNoQdRdGCK_gS7l4nZSDONbU` | Senad's locked baseline |
| 2 | **Master-Roadmap v1.2** | `1Ekf1f1HV1jS2PyLha5ERyh-JdmKTLgdY` | Living document — overrides any older roadmap |
| 3 | **SPRINT_BACKLOG.md v4** | `1pA6CBsZhgobILaUKp34ZHNiWcbOhBqH7` | What's done, what's next |
| 4 | **MAXICORE_CURRENT_STATE** | `1TzS_fmlEXrAx1TieAtLdaWtzd2ZvVuy6` | Live infra snapshot |
| 5 | **MAXICORE_SESSION_LOG** | `13QNbY9qou3Rjuca1Wt8Tz5CEUK1fMkic` | Recent sessions, prepend latest |
| 6 | **Forensik-Wiki Master** | `1Gl1miK4GZkkpPzZhSDpKM1u06E_7TQjp` | Manus-forensik entry-point |
| 7 | **MASTER_VM_INTEGRATION_SPEC_v1** | (lookup in roadmap §5) | For any VM-sprint |
| 8 | **SPRINT_PRE_FLIGHT_TEMPLATE_v1** | (lookup in roadmap §5) | Sprint-execution template |

**Also run:** `claude-mem search` with topic of current sprint before starting.

---

## 3. CURRENT STATE (snapshot — verify against Drive `CURRENT_STATE` for live)

- **Backend:** v0.3.9 SHA `cc55ba8` on 178.105.7.48
- **vm-rootfs:** main HEAD `14874ae`
- **sandbox-runtime in-VM:** v0.3.0
- **rootfs.ext4:** ubuntu-22.04-v14-b2-stub-v2 (UUID `b2a46355`)
- **Manus-Parity:** 52% gesamt, 55% Per-VM Sandbox L-B
- **In-VM endpoints:** 18 live | **Proxy-routes:** 12
- **Phase B:** 11/13 done (85%)
- **NEXT SPRINT:** B.3 Browser-Control (Drive Prompt `1iCLvs9GT_gL4W1CPWPRtYjN8TAcubVGu` v2)
- **Senad-Auftrag 12.05:** "bringe b bis zum ende" — Phase B Endgame aktiv (B.3 → B.7, ~70h, no detours)
- **Stage 0 (Forensik-RAG):** aufgeschoben bis Phase B done
- **HADES (91.98.188.46):** dormant, intakt, do NOT touch
- **Sharky:** 5/5 runners active

---

## 4. SPRINT WORKFLOW (mandatory steps)

1. **Pre-read** all 8 files from Section 2
2. **claude-mem search** for sprint topic — fetch prior context
3. **Forensik-Cross-Ref** if sprint touches Manus-pattern (Entschlüsselt_02_AGENT etc.)
4. **Implementation** in branch — never on main
5. **PR with banned-terms-check passing + SSH-signed-verified**
6. **CI must pass** before merge
7. **Sharky auto-deploy** (no manual deploy, no manual SSH)
8. **E2E full-suite** must be green
9. **Drift-check** — 3-Repo SHAs stable, HADES intakt, WireGuard mcwg0 untouched
10. **Status-MD upload to Drive** as `SPRINT_<ID>_<YYYY-MM-DD>.md`
11. **Backlog update:** `[ ]` → `[x]` + Drive-ID + Datum, version-bump to next backlog
12. **CURRENT_STATE.md overwrite** (HR-41)
13. **SESSION_LOG.md prepend** new entry (HR-41)
14. **claude-mem store** sprint outcomes

---

## 5. HARD RULES (44 total — full list in Master-Roadmap §4)

**Critical-must-never-violate:**
- **HR-1 to HR-21:** see Master-Roadmap (Hetzner-only, no Vercel/AWS/GCP, no FingerprintJS/Amplitude/Intercom, OpenClaw-only-agent, PR→CI→Merge→Sharky workflow, no direct SSH edits, etc.)
- **HR-22:** Browser-Init MUST Shadow-DOM force-open
- **HR-26:** Python 3.13.13, FastAPI 0.115.6, Patchright 1.58.2 EXACT pinning
- **HR-29:** Anti-Detection 2-Layer + en-US locale hardcoded
- **HR-32:** Manus-Parität-Ehrlichkeit (52% current, never inflate)
- **HR-33:** No FingerprintJS, no Amplitude, no Intercom
- **HR-34:** Pre-sprint MUST load Master-Roadmap + Forensik-Wiki
- **HR-35:** Every sprint passes 5Y-Vision-Compatibility-Check
- **HR-41:** "session log" trigger → 3-file-read pflicht
- **HR-42 (proposed):** Master-reports first, then detail-files

---

## 6. BANNED PATTERNS — IMMEDIATE STOP

- ❌ No `--admin`, no `--auto` flags
- ❌ No STOP-blocks in sprint-code
- ❌ No "Senad does X" — you decide + execute
- ❌ No Multi-Choice questions
- ❌ No Phase-X-later deferrals — finish what the sprint specifies
- ❌ No US-cloud touch (AWS / GCP / Vercel / Cloudflare Pages / Netlify)
- ❌ No proc-mode, no ProcessRunner, no simple_loop as agent fallback
- ❌ No FingerprintJS / Amplitude / Intercom
- ❌ No HELIX_12 / MaxiCore mentions in job-application docs
- ❌ No attorney-recommendations for Family-Court matter 229 F 85/26
- ❌ No reproducing API keys, tokens, secrets
- ❌ No Manus-Parität claims > 55% without forensic backing

---

## 7. REPO ARCHITECTURE (Helix12-Labs GitHub Org — 43 repos, 11 active)

**Active repos you may touch:**
- `helix12-maxicore-backend` (main backend, FastAPI)
- `helix12-maxicore-vm-rootfs` (Firecracker rootfs builder)
- `helix12-maxicore-sandbox-manager` (VM pool manager)
- `helix12-maxicore-infra-operator` (deployment ops)
- `helix12-maxicore-frontend` (Next.js 14)
- `helix12-maxicore-auth` (Zitadel integration)
- `helix12-maxicore-lexi` (VPS-Agent #1)
- `helix12-maxicore-skills` (DORMANT — Stage 1 wakeup)
- `helix12-maxicore-templates` (DORMANT — Stage 1 wakeup)
- + 2 weitere active

**33 dormant repos:** look but don't touch unless sprint specifies.

---

## 8. NETWORK TOPOLOGY (memorize)

```
maxicore-prod cloud 10.0.1.0/24:
  .1 backend-alt (EOL 22.05.2026)
  .2 frontend (77.42.31.244)
  .3 auth Zitadel (142.132.179.28)
  .4 backend ⭐ (178.105.7.48, v0.3.9)
  .5 lexi VPS-Agent (178.104.183.207)

vSwitch 10.10.0.0/24 VLAN4000:
  .2 HADES (91.98.188.46) — DORMANT, do NOT touch
  .3 PRIMARY (157.90.13.250)

VM-internal: 172.16.0.0/16 (MASQUERADE via PRIMARY eth0, B.2.NAT persistent)
WireGuard: mcwg0 (intern Backend↔VM-Pool)
```

---

## 9. PHASE B SPRINT-SEQUENCE (Senad-locked, do not reorder)

| # | Sprint | h | Endpoints | Status |
|---|---|---|---|---|
| 1 | **B.3 Browser-Control** | 6 | 6 | 🔴 NEXT — Drive `1iCLvs9GT_gL4W1CPWPRtYjN8TAcubVGu` |
| 2 | B.4 Terminal | 4 | 6 | ⏳ queued |
| 3 | B.5 Webdev+Slide+Skills | 36 | 23 | ⏳ queued |
| 4 | B.6 Mail (Gmail+Outlook) | 12 | 9 | ⏳ queued |
| 5 | B.7 VNC/Neko + LLM-Proxy | 12 | 4 | ⏳ queued |

**After B.7:** Phase B = 13/13, Manus-Parity ~75%, Stage 0 (Forensik-RAG) resumes.

---

## 10. FORENSIK SOURCE OF TRUTH

**Master entry-point:** `MaxiCore_Manus_Forensik_Wiki/` Drive folder (`1sLGV07_kq5Wxa3bTrPp0E9aiefmK9YZC`)

Inside:
- `00_MASTER_FORENSIK_WIKI.md` — entry
- `01_WAS_HABEN_ANDERE_UEBERSEHEN.md` — gap-analysis
- `02_MANUS_DRIVES_INDEX.md` — drive inventory

**Source-drives (rohdaten):**
- `manus/`, `manus2/`, `manus 3/`, `manus4/`, `Entschlüsselt/` (alle parallel zu Drive-Root)

**Key reports (Entschlüsselt/):**
- `Entschlüsselt_01_BACKEND.md` (Drive `1MEvYEWUZ3r_QX24k-Haa_Z2TDZu6Z6sH`)
- `Entschlüsselt_02_AGENT.md` (Drive `1flHICpsZY_P4Z6GBlgEDLxCUt6upLYGB`, 33 KB — class hierarchy 19×, 635 methods)
- `Entschlüsselt_03_FRONTEND.md` (Drive `1gryHf3Y5A5rRATXt1pt4POessBPziymI`)

**Forensik-Status-MD 42 (the deep-dive that found what others missed):**
- Drive `1wr9f2rXphQEWlqNVFiKAOWO8zaOgh473` — 21 structural findings

---

## 11. WHEN IN DOUBT

1. **STOP.** Don't guess.
2. **Re-read** Master-Roadmap §12 (current next step)
3. **claude-mem search** for prior context
4. **Read** Forensik-Wiki if architecture-question
5. **Ask Senad** only if hard-rule conflict — otherwise decide + execute

---

## 12. SUCCESS METRICS (what done looks like)

A sprint is **DONE** when:
- All endpoints/features specified in sprint-prompt are LIVE
- E2E full-suite is GREEN (no regressions)
- Backend tag bumped per backlog-convention
- 3-Repo SHAs stable, Apr-18-anchor on HADES intakt, WireGuard mcwg0 unchanged
- Banned-terms clean, SSH-signed verified=true
- Status-MD on Drive
- Backlog updated to next version
- CURRENT_STATE overwritten + SESSION_LOG prepended (HR-41)
- claude-mem stored
- Senad has Status-MD link

---

## 13. UPDATE PROTOCOL FOR THIS FILE

This CLAUDE.md gets a version-bump when:
- Major roadmap version changes (v1.X → v1.X+1)
- Hard-Rules added (HR-N+1)
- Phase changes (Phase B done → Phase C starts)
- Critical infra changes (new IP, new repo, etc.)

**Version-source-of-truth:** This file in **`helix12-maxicore-backend/CLAUDE.md`** is canonical.
**Mirror:** Drive ID will be linked here when uploaded. All 11 active repos must keep an up-to-date copy.

---

## 14. FAST-FACTS REFERENCE CARD

| What | Where |
|---|---|
| Current next sprint | Section 9 above + Drive Prompt |
| Live infra | CURRENT_STATE.md (Drive) |
| All decisions | Master-Roadmap §4 (Drive) |
| All hard-rules | Master-Roadmap §4 (Drive) |
| All forensik | MaxiCore_Manus_Forensik_Wiki/ (Drive) |
| Today's sessions | SESSION_LOG.md (Drive) |
| Backlog | SPRINT_BACKLOG.md v4 (Drive) |
| Anchor | Drive `1--fCTGabyNoQdRdGCK_gS7l4nZSDONbU` |

---

**EOF — Senad's voice:**
> "Statusberichte immer im Vergleich zu vorher aufschlüsseln, niemals lügen, immer ehrlich sein, sich selber nicht belügen und lieber 2x alles prüfen."

Apply this to every sprint output.