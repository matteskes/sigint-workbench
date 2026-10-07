<script lang="ts">
	// §20 wizard, §11 structure: step navigation is a persistent left
	// column (each step showing its save state), and SystemCheck is a
	// sticky footer visible on every step — this is where a broken
	// db/capture gets diagnosed, so the probes never scroll away.
	// Semantics unchanged: per-section save, inline field errors via
	// SettingsValidationError.field, restart-to-apply notice (D11).
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
	/** §11: last save error per section id — badges in the left column. */
	let sectionErrors = $state<Record<string, string>>({});
	/** §11: sections saved at least once this visit. */
	let savedSections = $state<Record<string, boolean>>({});

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
			savedSections = { ...savedSections, [section.id]: true };
			const errs = { ...sectionErrors };
			delete errs[section.id];
			sectionErrors = errs;
			return true;
		} catch (e) {
			const field = (e as { field?: string }).field ?? '';
			const msg = e instanceof Error ? e.message : String(e);
			error = { msg, field };
			sectionErrors = { ...sectionErrors, [section.id]: msg };
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
		if (step < index.sections.length) step += 1;
	}

	// §11 left column: moving between steps saves the dirty one first —
	// a failed save keeps you where you are, with the reason shown.
	async function goTo(i: number): Promise<void> {
		if (!index || i === step || busy) return;
		if (i > step) {
			for (let j = step; j < i; j++) {
				const section = index.sections[j];
				if (dirty(section)) {
					const ok = await save(section);
					if (!ok) return;
				}
			}
		} else {
			const section = index.sections[step];
			if (dirty(section)) {
				const ok = await save(section);
				if (!ok) return;
			}
		}
		error = null;
		step = i;
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

	function stepBadge(section: SettingsSection): { label: string; class: string } {
		if (sectionErrors[section.id]) return { label: 'error', class: 'text-red-400' };
		if (dirty(section)) return { label: 'dirty', class: 'text-amber-400' };
		if (savedSections[section.id]) return { label: 'saved', class: 'text-green-400' };
		return { label: 'clean', class: 'text-slate-500' };
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<!-- §11: the wizard body is the scroll container; the step column and the
	SystemCheck footer stay visible while long section forms scroll. -->
	<div class="mx-auto w-full max-w-5xl min-h-0 flex-1 overflow-y-auto px-6 py-6">
		<h1 class="text-xl font-bold">Setup</h1>
		<p class="mt-1 text-sm text-slate-400">
			Configure the workbench without hand-editing YAML (SPEC §20).
			Saves take effect when the affected services restart.
		</p>

		{#if !index}
			<p class="mt-6 text-sm text-slate-500">Loading configuration…</p>
		{:else}
			<div class="mt-5 flex items-start gap-6">
				<!-- §11: persistent step column with save-state badges; sticky so it
				stays visible while the section form scrolls. -->
				<nav class="sticky top-6 w-56 shrink-0" aria-label="Setup sections">
					<ol class="space-y-1">
						{#each index.sections as section, i (section.id)}
							<li>
								<button
									class="flex w-full items-center justify-between rounded px-2.5 py-1.5 text-left text-sm
										{i === step
										? 'bg-slate-800 text-slate-100'
										: 'text-slate-400 hover:bg-slate-800/60'}"
									aria-current={i === step ? 'step' : undefined}
									onclick={() => goTo(i)}
								>
									<span class="truncate">{i + 1}. {section.label}</span>
									<span class="ml-2 shrink-0 text-[10px] {stepBadge(section).class}">
										{stepBadge(section).label}
									</span>
								</button>
							</li>
						{/each}
						<li>
							<button
								class="flex w-full items-center rounded px-2.5 py-1.5 text-left text-sm
									{step >= index.sections.length
									? 'bg-slate-800 text-slate-100'
									: 'text-slate-400 hover:bg-slate-800/60'}"
								onclick={() => {
									if (index) goTo(index.sections.length);
								}}
							>
								{index.sections.length + 1}. Finish
							</button>
						</li>
					</ol>
				</nav>

				<!-- Current section -->
				<div class="min-w-0 flex-1">
					{#if step < index.sections.length}
						{@const section = index.sections[step]}
						<div class="rounded border border-slate-700 bg-slate-800/40 p-5">
							<div class="flex items-baseline justify-between">
								<h2 class="text-lg font-semibold">{section.label}</h2>
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
								<button
									class="rounded bg-sky-700 px-4 py-1 text-sm font-medium hover:bg-sky-600 disabled:opacity-50"
									disabled={busy}
									onclick={next}>{dirty(section) ? 'Save & continue' : 'Save section'}</button
								>
								{#if busy}<span class="text-xs text-slate-500">saving…</span>{/if}
								{#if sectionErrors[section.id]}
									<span class="text-xs text-red-400">last save failed</span>
								{/if}
							</div>
						</div>
					{:else}
						<div class="rounded border border-slate-700 bg-slate-800/40 p-5">
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
									class="rounded bg-green-700 px-4 py-1 text-sm font-medium hover:bg-green-600 disabled:opacity-50"
									disabled={busy}
									onclick={finish}>Open the dashboard</button
								>
							</div>
						</div>
					{/if}
				</div>
			</div>
		{/if}
	</div>

	<!-- §11: persistent SystemCheck footer — probes on every step -->
	<footer class="sticky bottom-0 border-t border-slate-700 bg-slate-900/95 px-6 py-2 backdrop-blur">
		<SystemCheck {status} />
	</footer>
</div>
