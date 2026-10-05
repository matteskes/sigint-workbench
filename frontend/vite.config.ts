import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit(), tailwindcss()],
	server: {
		proxy: {
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: true
			},
			'/ws': {
				// api-gateway is the single client WS ingress (§2.2 A3);
				// ws-hub itself is internal-only on the compose network.
				target: 'ws://localhost:8080',
				ws: true
			}
		}
	}
});