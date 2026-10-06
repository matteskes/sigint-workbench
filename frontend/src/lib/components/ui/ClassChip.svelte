<script lang="ts">
	// §4/§13: class label rendered in its palette color — the single
	// consumer path into classColor.ts for text surfaces.
	import { classText } from '$lib/ui/classColor';

	let { cls, size = 'sm' }: { cls: string; size?: 'xs' | 'sm' } = $props();
</script>

<span
	class="inline-flex items-center rounded-full bg-slate-800 px-2 py-0.5 capitalize
		{size === 'xs' ? 'text-[10px]' : 'text-xs'} {classText(cls)}"
>{cls.replace(/_/g, ' ')}</span>
