<script lang="ts">
	import type { SettingsField } from '$lib/api/client';

	let {
		field,
		value,
		onchange
	}: {
		field: SettingsField;
		value: unknown;
		onchange: (key: string, v: unknown) => void;
	} = $props();

	// Absent optional values display the documented default (if any).
	const shown = $derived(value === undefined || value === null ? field.default : value);

	function asText(v: unknown): string {
		return v === undefined || v === null ? '' : String(v);
	}

	function numeric(raw: string): unknown {
		if (raw === '') return field.optional ? undefined : raw;
		return Number(raw);
	}
</script>

<label class="block">
	<span class="flex items-baseline gap-1 text-sm text-slate-300">
		{field.label}
		{#if field.unit}<span class="text-xs text-slate-500">{field.unit}</span>{/if}
		{#if field.optional}<span class="text-xs text-slate-600">(optional)</span>{/if}
	</span>
	{#if field.type === 'select'}
		<select
			class="mt-1 w-full rounded border border-slate-600 bg-slate-800 px-2 py-1.5 text-sm"
			value={asText(shown)}
			onchange={(e) => onchange(field.key, (e.currentTarget as HTMLSelectElement).value)}
		>
			{#each field.options ?? [] as opt}
				<option value={opt}>{opt}</option>
			{/each}
		</select>
	{:else if field.type === 'bool'}
		<input
			type="checkbox"
			class="mt-2"
			checked={shown === true}
			onchange={(e) => onchange(field.key, (e.currentTarget as HTMLInputElement).checked)}
		/>
	{:else if field.type === 'number'}
		<input
			type="number"
			class="mt-1 w-full rounded border border-slate-600 bg-slate-800 px-2 py-1.5 text-sm"
			step={field.step ?? (field.integer ? 1 : 'any')}
			min={field.min}
			max={field.max}
			placeholder={field.optional ? 'unset' : ''}
			value={asText(shown)}
			onchange={(e) => onchange(field.key, numeric((e.currentTarget as HTMLInputElement).value))}
		/>
	{:else}
		<input
			type="text"
			class="mt-1 w-full rounded border border-slate-600 bg-slate-800 px-2 py-1.5 text-sm"
			placeholder={field.optional ? 'unset' : ''}
			value={asText(shown)}
			onchange={(e) => onchange(field.key, (e.currentTarget as HTMLInputElement).value)}
		/>
	{/if}
	{#if field.help}<span class="mt-1 block text-xs text-slate-500">{field.help}</span>{/if}
	{#if field.warning}
		<span class="mt-1 block text-xs text-amber-400">⚠ {field.warning}</span>
	{/if}
</label>
