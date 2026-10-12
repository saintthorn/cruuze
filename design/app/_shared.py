# Shared pieces of the app mockups: the five themes as CSS variables, the
# theme switcher, and the Sail mark. build.py stitches them into each page.
FONTS = '<link rel="preconnect" href="https://fonts.googleapis.com"><link href="https://fonts.googleapis.com/css2?family=Geist:wght@300..700&family=Geist+Mono:wght@400..600&family=JetBrains+Mono:wght@400..700&family=Roboto:wght@400..700&family=EB+Garamond:wght@400..700&family=Cinzel:wght@500..800&family=Exo+2:wght@400..700&family=Orbitron:wght@500..800&display=swap" rel="stylesheet">'

TOKENS = """
  :root, [data-theme='midnight'] { --bg:#08090c; --rail:#050608; --panel:#0f1014; --hover:#15161c; --active:#1d1e26; --line:#1a1b22; --line-2:#2b2c36; --fg:#f4f2ec; --muted:#a3a09a; --faint:#6a6862; --accent:#dc2b3d; --accent-2:#a8321f; --accent-fg:#fff8f5; --ok:#3ecf8e; --warn:#f0b35a; --font:'Geist',ui-sans-serif,system-ui,sans-serif; --display:var(--font); --r:14px; --glow:rgb(220 43 61 / .22); --caps:none; --track:-.015em; color-scheme:dark; }
  [data-theme='hacker'] { --bg:#0a0d0c; --rail:#070909; --panel:#0e1311; --hover:#121816; --active:#17201c; --line:#18211d; --line-2:#25332d; --fg:#c4e3d0; --muted:#7c988a; --faint:#4c6257; --accent:#2fe07f; --accent-2:#1c9a55; --accent-fg:#04140b; --ok:#2fe07f; --warn:#e5b567; --font:'JetBrains Mono',ui-monospace,Menlo,monospace; --display:var(--font); --r:3px; --glow:rgb(47 224 127 / .12); --caps:none; --track:0; color-scheme:dark; }
  [data-theme='material'] { --bg:#f0f4f9; --rail:#e4eaf3; --panel:#ffffff; --hover:#e9eef6; --active:#d3e3fd; --line:#e1e3e8; --line-2:#c4c7c5; --fg:#1f1f1f; --muted:#444746; --faint:#747775; --accent:#0b57d0; --accent-2:#0842a0; --accent-fg:#ffffff; --ok:#146c2e; --warn:#9a5b00; --font:'Roboto',ui-sans-serif,system-ui,sans-serif; --display:var(--font); --r:20px; --glow:rgb(11 87 208 / .1); --caps:none; --track:0; color-scheme:light; }
  [data-theme='quest'] { --bg:#e9dab8; --rail:#dfcda3; --panel:#f6ecd4; --hover:#e2d0a6; --active:#d6bf8c; --line:#cdb887; --line-2:#a68a52; --fg:#2a1b0c; --muted:#5c4729; --faint:#86704a; --accent:#8c1c13; --accent-2:#5e120c; --accent-fg:#f8ecd0; --ok:#3a6a2c; --warn:#8f5f00; --font:'EB Garamond',Palatino,Georgia,serif; --display:'Cinzel','EB Garamond',serif; --r:5px; --glow:rgb(176 132 42 / .25); --caps:none; --track:.01em; color-scheme:light; }
  [data-theme='hyperdrive'] { --bg:#05070d; --rail:#03050a; --panel:#0a101c; --hover:#0f1828; --active:#15233b; --line:#13203a; --line-2:#214070; --fg:#cfe8ff; --muted:#7f9fc6; --faint:#4a658c; --accent:#ffb62e; --accent-2:#c9781a; --accent-fg:#1a1000; --ok:#5ef2c0; --warn:#ff8f3d; --font:'Exo 2',ui-sans-serif,system-ui,sans-serif; --display:'Orbitron','Exo 2',sans-serif; --r:0px; --glow:rgb(79 216 255 / .16); --caps:uppercase; --track:.02em; color-scheme:dark; }
  [data-theme='quest'] body { font-size: 16.5px; }
  * { box-sizing: border-box; margin: 0; }
  html, body { height: 100%; }
  body { background: radial-gradient(ellipse 70% 50% at 50% -10%, var(--glow), transparent 70%), var(--bg); color: var(--fg); font: 14px/1.45 var(--font); letter-spacing: var(--track); -webkit-font-smoothing: antialiased; overflow: hidden; transition: background .5s, color .3s; }
  button { background: none; border: 0; color: inherit; cursor: pointer; font: inherit; letter-spacing: inherit; }
  .mono { font-family: 'Geist Mono', ui-monospace, 'SF Mono', Menlo, monospace; letter-spacing: 0; }
  h1, h2, h3 { font-family: var(--display); text-transform: var(--caps); }
  .themes { align-items: center; backdrop-filter: blur(14px); background: color-mix(in srgb, var(--panel) 80%, transparent); border: 1px solid var(--line-2); border-radius: 999px; bottom: 16px; box-shadow: 0 18px 40px -18px #000; display: flex; gap: 6px; padding: 6px 8px 6px 14px; position: fixed; right: 16px; z-index: 50; }
  .themes span { color: var(--muted); font-size: 12px; margin-right: 4px; }
  .themes button { border: 2px solid transparent; border-radius: 50%; height: 22px; transition: transform .2s; width: 22px; }
  .themes button:hover { transform: scale(1.15); }
  .themes button[aria-pressed='true'] { border-color: var(--fg); }
"""

SWITCHER = """<div class="themes" role="group" aria-label="Theme"><span id="theme-name">mode=midnight</span>
  <button data-set="midnight" aria-label="mode=midnight" style="background:linear-gradient(135deg,#08090c 50%,#dc2b3d 50%)"></button>
  <button data-set="hacker" aria-label="mode=hacker" style="background:linear-gradient(135deg,#0a0d0c 50%,#2fe07f 50%)"></button>
  <button data-set="material" aria-label="mode=material" style="background:linear-gradient(135deg,#ffffff 50%,#0b57d0 50%)"></button>
  <button data-set="quest" aria-label="mode=quest" style="background:linear-gradient(135deg,#efe2c4 50%,#8c1c13 50%)"></button>
  <button data-set="hyperdrive" aria-label="mode=hyperdrive" style="background:linear-gradient(135deg,#05070d 50%,#ffb62e 50%)"></button>
</div>
<script>
  const setTheme = (id) => {
    document.documentElement.dataset.theme = id
    document.getElementById('theme-name').textContent = 'mode=' + id
    document.querySelectorAll('.themes button').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.set === id)))
  }
  document.querySelectorAll('.themes button').forEach((b) => b.addEventListener('click', () => setTheme(b.dataset.set)))
  setTheme('midnight')
</script>"""

MARK = '<svg class="mark" viewBox="0 0 64 64" aria-hidden="true"><path d="M29 9c15 11 23 23 26 37H13z" fill="var(--fg)" stroke="var(--fg)" stroke-width="3" stroke-linejoin="round"/><path d="M11 56h44" fill="none" stroke="var(--accent)" stroke-width="6.5" stroke-linecap="round"/></svg>'

def icon(d):
    return f'<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">{d}</svg>'
ICONS = {
  'inbox': icon('<path d="M3 13l3-8h12l3 8v5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/><path d="M3 13h5l1 3h6l1-3h5"/>'),
  'focus': icon('<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4"/>'),
  'issues': icon('<circle cx="12" cy="12" r="9"/><path d="M8.5 12.5l2.5 2.5 4.5-5.5"/>'),
  'cycles': icon('<path d="M4 12a8 8 0 0 1 13.7-5.6L20 9"/><path d="M20 4v5h-5"/><path d="M20 12a8 8 0 0 1-13.7 5.6L4 15"/><path d="M4 20v-5h5"/>'),
  'roadmap': icon('<path d="M4 6h9M8 12h12M4 18h7"/>'),
  'insights': icon('<path d="M5 20V10M12 20V4M19 20v-7"/>'),
  'search': icon('<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>'),
  'plus': icon('<path d="M12 5v14M5 12h14"/>'),
  'branch': icon('<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 7v10M18 10c0 4-6 3-10.5 6"/>'),
  'play': icon('<path d="M7 5l12 7-12 7z" fill="currentColor"/>'),
}
