<script lang="ts">
	import type { SettingsListField } from '$lib/api/client';
	import FieldInput from './FieldInput.svelte';

	let {
		list,
		items,
		onchange
	}: {
		list: SettingsListField;
		items: Record<string, unknown>[];
		onchange: (items: Record<string, unknown>[]) => void;
	} = $props();

	function update(idx: number, key: string, v: unknown): void {
		onchange(
			items.map((it, i) => {
				if (i !== idx) return it;
				const copy = { ...it };
				if (v === undefined) delete copy[key];
				else copy[key] = v;
				return copy;
			})
		);
	}

	// New devices start from the schema defaults so the card is valid
	// as-submitted (the server requires every non-optional field).
	function add(): void {
		const item: Record<string, unknown> = {};
		for (const f of list.itemFields) {
			if (f.default !== undefined) item[f.key] = f.default;
		}
		item[list.itemLabelKey] = `sdr-${items.length + 1}`;
		onchange([...items, item]);
	}

	function remove(idx: number): void {
		onchange(items.filter((_, i) => i !== idx));
	}
</script>

<div class="space-y-3">
	{#each items as item, idx}
		<div class="rounded border border-slate-700 bg-slate-800/50 p-3">
			<div class="flex items-center justify-between">
				<span class="font-mono text-sm text-blue-300">{item[list.itemLabelKey] ?? '?'}</span>
				<button
					class="rounded bg-red-900/60 px-2 py-0.5 text-xs text-red-200 hover:bg-red-800"
					onclick={() => remove(idx)}>Remove</button
				>
			</div>
			<div class="mt-2 grid grid-cols-2 gap-3">
				{#each list.itemFields as f}
					<FieldInput field={f} value={item[f.key]} onchange={(k, v) => update(idx, k, v)} />
				{/each}
			</div>
		</div>
	{/each}
	<button class="rounded bg-blue-700 px-3 py-1 text-sm hover:bg-blue-600" onclick={add}>
		Add device
	</button>
	{#if list.help}<p class="mt-2 text-xs text-slate-500">{list.help}</p>{/if}
</div>
