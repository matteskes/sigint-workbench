<script lang="ts">
	// §16: status is never color-only — a pill always carries its text.
	type Tone = 'ok' | 'warn' | 'danger' | 'muted';

	let {
		tone,
		label,
		dot = false,
		title
	}: { tone: Tone; label: string; dot?: boolean; title?: string } = $props();

	const TONE_CLASS: Record<Tone, string> = {
		ok: 'bg-green-900/50 text-green-300',
		warn: 'bg-amber-900/50 text-amber-300',
		danger: 'bg-red-900/50 text-red-300',
		muted: 'bg-slate-800 text-slate-400'
	};
	const DOT_CLASS: Record<Tone, string> = {
		ok: 'bg-green-400',
		warn: 'bg-amber-400',
		danger: 'bg-red-400',
		muted: 'bg-slate-500'
	};
</script>

<span class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] {TONE_CLASS[tone]}" title={title}>
	{#if dot}<span class="h-1.5 w-1.5 rounded-full {DOT_CLASS[tone]}"></span>{/if}
	{label}
</span>
