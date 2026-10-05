<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import {
		fetchSettings,
		fetchSetupStatus,
		saveSettings,
		completeSetup,
		type SettingsIndex,
		type SetupStatus,
		type SettingsSection,
		type SettingsValues
	} from '$lib/api/client';
	import FieldInput from '$lib/components/setup/FieldInput.svelte';
	import SdrListEditor from '$lib/components/setup/SdrListEditor.svelte';
	import SystemCheck from '$lib/components/setup/SystemCheck.svelte';

	let index = $state<SettingsIndex | null>(null);
	let status = $state<SetupStatus | null>(null);
	let step = $state(0);
	let drafts = $state<Record<string, SettingsValues>>({});
	let busy = $state(false);
	let error = $state<{ msg: string; field: string } | null>(null);

	onMount(() => {
		fetchSettings()
			.then((idx) => {
				index = idx;
				const d: Record<string, SettingsValues> = {};
				for (const s of idx.sections) d[s.id] = structuredClone(idx.values[s.id] ?? {});
				drafts = d;
			})
			.catch((e) => console.error(e));
		fetchSetupStatus()
			.then((st) => (status = st))
			.catch((e) => console.error(e));
	});

	function setField(sectionId: string, key: string, v: unknown): void {
		drafts = { ...drafts, [sectionId]: { ...drafts[sectionId], [key]: v } };
	}

	function dirty(section: SettingsSection): boolean {
		return JSON.stringify(drafts[section.id]) !== JSON.stringify(index?.values[section.id] ?? {});
	}

	function groupsOf(section: SettingsSection): [string, SettingsSection['fields']][] {
		const byGroup = new Map<string, SettingsSection['fields']>();
		for (const f of section.fields) {
			const arr = byGroup.get(f.group) ?? [];
			arr.push(f);
			byGroup.set(f.group, arr);
		}
		return [...byGroup.entries()];
	}

	async function save(section: SettingsSection): Promise<boolean> {
		busy = true;
		error = null;
		try {
			await saveSettings(section.id, drafts[section.id] ?? {});
			if (index) {
				index = {
					...index,
					values: { ...index.values, [section.id]: structuredClone(drafts[section.id] ?? {}) }
				};
			}
			return true;
		} catch (e) {
			const field = (e as { field?: string }).field ?? '';
			error = { msg: e instanceof Error ? e.message : String(e), field };
			return false;
		} finally {
			busy = false;
		}
	}

	// Per-section save as you step through the wizard (§20: restart to
	// apply — a failed save keeps you on the section with the reason).
	async function next(): Promise<void> {
		if (!index) return;
		const section = index.sections[step];
		if (dirty(section)) {
			const ok = await save(section);
			if (!ok) return;
		}
		step += 1;
	}

	const restartSet = $derived(
		index ? [...new Set(index.sections.flatMap((s) => s.restart))].join(' ') : ''
	);

	async function finish(): Promise<void> {
		busy = true;
		try {
			await completeSetup(false);
			await goto('/');
		} finally {
			busy = false;
		}
	}
</script>

<div class="mx-auto max-w-3xl px-6 py-8">
	<h1 class="text-xl font-bold">Setup</h1>
	<p class="mt-1 text-sm text-slate-400">
		Configure the workbench without hand-editing YAML (SPEC §20).
		Saves take effect when the affected services restart.
	</p>
	<div class="mt-4">
		<SystemCheck status={status} />
	</div>

	{#if !index}
		<p class="mt-6 text-sm text-slate-500">Loading configuration…</p>
	{:else if step < index.sections.length}
		{@const section = index.sections[step]}
		<div class="mt-6 rounded border border-slate-700 bg-slate-800/40 p-5">
			<div class="flex items-baseline justify-between">
				<h2 class="text-lg font-semibold">{step + 1}. {section.label}</h2>
				{#if dirty(section)}<span class="text-xs text-amber-400">unsaved changes</span>{/if}
			</div>
			<p class="mt-1 text-xs text-slate-500">{section.description}</p>
			<p class="mt-0.5 text-xs text-slate-600">file: {section.file}</p>

			{#each section.lists ?? [] as list}
				<div class="mt-4">
					<h3 class="text-sm font-semibold text-slate-300">{list.label}</h3>
					<SdrListEditor
						list={list}
						items={(drafts[section.id]?.[list.key] as Record<string, unknown>[]) ?? []}
						onchange={(items) => setField(section.id, list.key, items)}
					/>
				</div>
			{/each}

			{#each groupsOf(section) as [group, fields]}
				<div class="mt-4">
					<h3 class="text-sm font-semibold text-slate-300">{group}</h3>
					<div class="mt-2 grid grid-cols-2 gap-4">
						{#each fields as f}
							<FieldInput
								field={f}
								value={drafts[section.id]?.[f.key]}
								onchange={(k, v) => setField(section.id, k, v)}
							/>
						{/each}
					</div>
				</div>
			{/each}

			{#if error}
				<p class="mt-3 rounded bg-red-900/50 px-3 py-2 text-sm text-red-200">
					{error.msg}{error.field ? ` (field: ${error.field})` : ''}
				</p>
			{/if}

			<div class="mt-5 flex items-center gap-3">
				{#if step > 0}
					<button
						class="rounded border border-slate-600 px-3 py-1 text-sm hover:bg-slate-700"
						onclick={() => {
							step -= 1;
							error = null;
						}}>Back</button
					>
				{/if}
				<button
					class="rounded bg-blue-700 px-4 py-1 text-sm font-medium hover:bg-blue-600 disabled:opacity-50"
					disabled={busy}
					onclick={next}>{dirty(section) ? 'Save & continue' : 'Continue'}</button
				>
				{#if busy}<span class="text-xs text-slate-500">saving…</span>{/if}
			</div>
		</div>
	{:else}
		<div class="mt-6 rounded border border-slate-700 bg-slate-800/40 p-5">
			<h2 class="text-lg font-semibold">Done — restart to apply</h2>
			<p class="mt-2 text-sm text-slate-300">
				Saved sections take effect after the affected services restart:
			</p>
			<pre class="mt-2 overflow-x-auto rounded bg-slate-900 px-3 py-2 text-xs text-slate-300">
				docker compose restart {restartSet}
			</pre>
			<p class="mt-2 text-xs text-slate-500">
				On macOS dev (native processes), restart the listed binaries the
				same way you started them.
			</p>
			<div class="mt-5 flex items-center gap-3">
				<button
					class="rounded border border-slate-600 px-3 py-1 text-sm hover:bg-slate-700"
					onclick={() => (step -= 1)}>Back</button
				>
				<button
					class="rounded bg-green-700 px-4 py-1 text-sm font-medium hover:bg-green-600 disabled:opacity-50"
					disabled={busy}
					onclick={finish}>Open the dashboard</button
				>
			</div>
		</div>
	{/if}
</div>
