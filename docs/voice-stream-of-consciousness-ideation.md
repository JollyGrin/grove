# Voice / stream-of-consciousness capture — ideation

Status: **raw ideation, not a design doc yet**. Captured 2026-07-08, to be
elaborated later with a bigger model. Not a ticket — no implementation
implied by this doc's existence.

## Original prompt (reformatted from a spoken stream-of-consciousness)

> Grove is letting me churn through tickets faster than before, so the
> new bottleneck is getting tickets *in*. I use Soto to transcribe my
> voice to text, and I want a workflow built around that.
>
> The idea: let me turn on transcription and just talk — openly, messily,
> no pressure to phrase things correctly the first time. I'll probably
> ramble, backtrack, and correct myself mid-thought, the way anyone
> thinks out loud about something. I want a mode where I can do that
> without worrying that the mess will end up in the output, because I
> trust it to capture the actual intent and throw away the fluff,
> false starts, and self-corrections.
>
> That raw transcript needs a step that distills it into what I would
> have written if I'd taken the time to think it through clearly —
> basically the same shape as the input to our existing ticket-creation
> flow (which then asks clarifying questions, etc.). This is the step
> *before* that: a way to get from "talking out loud" to "a clean
> written idea," which then can optionally feed into ticket-making.
>
> I want this as an invokable skill/mode — something I can say to
> activate ("stream of consciousness mode," "voice mode," etc.) that
> tells the assistant: I'm about to just talk, don't interrupt, take
> the whole blob afterward, distill it, and produce an ideation doc
> from it. Keep it a distinct, separate step from ticket-making — I
> might want to just capture the idea and let it sit, then come back to
> it later and turn it into a ticket, rather than always chaining
> straight into ticket creation.

## 1. Core shape — two distinct stages

- **Stage A — capture**: a mode that accepts a long, unstructured,
  self-correcting spoken/transcribed monologue and does *not* try to
  act on it or ask clarifying questions mid-stream. Purely absorbs.
- **Stage B — distill**: turns the raw blob into a clean ideation doc —
  the version "as if I'd taken time to think it through clearly." Output
  should read like the two existing hand-written ideation docs in
  `docs/` (this one and `multi-model-orchestration-ideation.md`): a
  reformatted "original prompt," structured sections, open questions.
- Explicitly **not** the same step as ticket-making. Ticket-making
  (existing flow: distill → ask clarifying questions → ticket) may
  consume a Stage B doc later, on request — but Stage B should be able
  to stand alone and just get saved for later.

*(fill in: is Stage B always synchronous right after Stage A, or could
Stage A alone be "committed" and distilled in a separate pass — e.g.
dictate three separate ideas across a day, distill all three at once
in the evening?)*

## 2. Activation phrasing — candidate trigger phrases

Brainstormed candidates (bigger model should pressure-test these against
false-positive rates in normal conversation):

- "Stream of consciousness mode" / "stream of consciousness"
- "Voice mode" / "let me just talk"
- "Let me think out loud"
- "Brain dump" / "let me brain dump"
- "Ideation mode"
- "Just capture this, don't act on it yet"

Open question: single fixed phrase (reliable, low ambiguity) vs. a
looser intent-match (more natural, riskier false-trigger). Given this is
voice-transcribed input, favor a phrase unlikely to occur naturally
mid-ramble ("stream of consciousness mode" is distinctive; "let me just
talk" might trigger accidentally).

## 3. Distillation behavior — what "clean up" means concretely

*(fill in: how aggressively to cut repetition/self-correction vs.
preserve nuance the speaker circled back to add; how to handle the
speaker explicitly retracting an earlier statement mid-monologue — e.g.
"actually no, scratch that" — does the model need to detect retractions
specifically, or is that just normal distillation; whether to preserve
a lightly-cleaned verbatim quote of the original alongside the distilled
version, the way the two existing ideation docs do)*

## 4. Output / storage — where it lands

- Existing precedent: `docs/*-ideation.md`, following the format seen in
  `multi-model-orchestration-ideation.md` (status line, reformatted
  quote, numbered sections with fill-ins, open questions).
- Should stay a lightweight, low-ceremony artifact — the entire point is
  removing friction between "I have a thought" and "it's captured
  somewhere safe," so the capture step shouldn't demand structure.

*(fill in: naming convention for the file when the topic isn't obvious
in one sentence; whether these accumulate in `docs/` indefinitely or get
triaged/archived; whether this should be a repo-agnostic Claude Code
skill vs. something specific to the grove repo)*

## 5. Relationship to ticket-making

- Ticket-making already exists as a flow: raw idea → clarifying
  questions → ticket. This ideation-capture step is meant to sit
  *before* that flow, producing the "raw idea" input to it — but as an
  optional, separate, saveable artifact rather than something that must
  immediately continue into ticket creation.
- Should support both: (a) capture → distill → stop, saved for later,
  and (b) capture → distill → immediately hand off into ticket-making,
  in one sitting, when the speaker wants that.

## Open questions

- Does this need to be voice-specific at all, or is it really just
  "loose unstructured input mode" that happens to be triggered by voice
  dictation? (Typed rambling would have the same shape.)
- Should activation be a Claude Code skill (repo-agnostic, portable
  across projects) or something narrower?
- How does this interact with Soto itself — is Soto purely the
  speech-to-text layer feeding a normal chat turn, or does the capture
  mode need awareness of Soto's own segmentation/punctuation behavior?
- Multiple captured-but-undistilled ideas at once — does Stage A support
  queuing several before running Stage B, or is it strictly one
  monologue in, one doc out?
