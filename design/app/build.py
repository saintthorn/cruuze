#!/usr/bin/env python3
# Builds the two app mockups as self-contained pages:
#   python3 design/app/build.py
# 1-deck.html   a command bar, a floating rail, and an issue list with a live peek
# 2-flow.html   a home that says what to do next, above a board
from pathlib import Path

from _shared import FONTS, ICONS, MARK, SWITCHER, TOKENS

HERE = Path(__file__).parent


def page(title, css, body, script=''):
    return f'''<!doctype html>
<html lang="en" data-theme="midnight">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
{FONTS}
<style>{TOKENS}{css}</style>
</head>
<body>
{body}
{SWITCHER}
{script}
</body>
</html>
'''


# ---------------------------------------------------------------- 1: Deck

DECK_CSS = """
  .app { display: grid; gap: 12px; grid-template: 56px 1fr / 60px 1fr; height: 100%; padding: 12px; }
  .glass { backdrop-filter: blur(16px); background: color-mix(in srgb, var(--panel) 86%, transparent); border: 1px solid var(--line); border-radius: var(--r); }
  .top { align-items: center; display: flex; gap: 14px; grid-column: 1 / -1; padding: 0 10px 0 16px; }
  .mark { height: 24px; width: 24px; }
  .ws { align-items: center; border-radius: calc(var(--r) * .7); display: flex; font-weight: 600; gap: 8px; padding: 6px 10px; }
  .ws:hover { background: var(--hover); }
  .ws small { color: var(--faint); font-weight: 400; }
  .cmd { align-items: center; background: var(--bg); border: 1px solid var(--line-2); border-radius: 999px; color: var(--faint); display: flex; flex: 1; gap: 10px; height: 38px; margin: 0 auto; max-width: 560px; padding: 0 8px 0 14px; transition: border-color .2s, box-shadow .2s; }
  .cmd:hover { border-color: var(--accent); box-shadow: 0 0 0 4px var(--glow); }
  .cmd kbd { background: var(--hover); border-radius: 6px; color: var(--muted); font: 11px 'Geist Mono', ui-monospace, monospace; margin-left: auto; padding: 3px 7px; }
  .seg { background: var(--bg); border: 1px solid var(--line); border-radius: 999px; display: flex; padding: 3px; }
  .seg button { border-radius: 999px; color: var(--muted); font-size: 13px; padding: 5px 13px; transition: background .2s, color .2s; }
  .seg button[aria-pressed='true'] { background: var(--active); color: var(--fg); }
  .new { align-items: center; background: linear-gradient(160deg, var(--accent), var(--accent-2)); border-radius: 999px; box-shadow: 0 8px 20px -10px var(--accent); color: var(--accent-fg); display: flex; font-weight: 600; gap: 6px; height: 36px; padding: 0 16px 0 12px; transition: transform .2s; }
  .new:hover { transform: translateY(-1px); }
  .rail { align-items: center; display: flex; flex-direction: column; gap: 6px; padding: 10px 0; }
  .rail button { align-items: center; border-radius: calc(var(--r) * .7); color: var(--muted); display: flex; height: 40px; justify-content: center; position: relative; transition: background .2s, color .2s; width: 40px; }
  .rail button:hover { background: var(--hover); color: var(--fg); }
  .rail button[aria-current] { background: var(--active); color: var(--fg); }
  .rail button[aria-current]::before { background: var(--accent); border-radius: 2px; content: ''; height: 18px; left: -10px; position: absolute; width: 3px; }
  .rail b { background: var(--accent); border-radius: 99px; color: var(--accent-fg); font-size: 10px; min-width: 16px; padding: 0 4px; position: absolute; right: 1px; top: 1px; }
  .rail .me { background: var(--active); border: 1px solid var(--line-2); border-radius: 50%; font-size: 12px; font-weight: 600; height: 34px; margin-top: auto; width: 34px; }
  .work { display: grid; gap: 12px; grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr); min-height: 0; }
  .pane { display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
  .cycle { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 18px; padding: 16px 20px; }
  .cycle h1 { font-size: 17px; font-weight: 600; }
  .cycle p { color: var(--muted); font-size: 12.5px; margin-top: 2px; }
  .meter { background: var(--hover); border-radius: 99px; flex: 1; height: 6px; overflow: hidden; }
  .meter i { background: linear-gradient(90deg, var(--accent-2), var(--accent)); border-radius: 99px; display: block; height: 100%; width: 62%; }
  .stat { text-align: right; }
  .stat b { display: block; font-size: 16px; font-weight: 600; }
  .stat span { color: var(--faint); font-size: 11.5px; }
  .scroll { flex: 1; overflow-y: auto; padding: 8px; scrollbar-width: thin; }
  .grp { align-items: center; color: var(--muted); display: flex; font-size: 12px; font-weight: 600; gap: 8px; padding: 14px 12px 6px; }
  .grp i { border-radius: 50%; height: 8px; width: 8px; }
  .grp span { color: var(--faint); font-weight: 400; }
  .item { align-items: center; border: 1px solid transparent; border-radius: calc(var(--r) * .8); display: flex; gap: 12px; padding: 11px 12px; position: relative; text-align: left; transition: background .18s, border-color .18s, transform .18s; width: 100%; }
  .item:hover { background: var(--hover); transform: translateX(2px); }
  .item[aria-selected='true'] { background: var(--active); border-color: var(--line-2); box-shadow: inset 3px 0 0 var(--accent); }
  .item .id { color: var(--faint); font-size: 12px; width: 58px; }
  .item .t { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tag { align-items: center; border: 1px solid var(--line-2); border-radius: 999px; color: var(--muted); display: inline-flex; font-size: 11.5px; gap: 5px; padding: 2px 9px; white-space: nowrap; }
  .tag svg { height: 12px; width: 12px; }
  .av { align-items: center; background: color-mix(in srgb, var(--accent) 22%, var(--panel)); border-radius: 50%; display: inline-flex; flex: none; font-size: 10px; font-weight: 700; height: 24px; justify-content: center; width: 24px; }
  .peek { animation: slide .35s cubic-bezier(.2,.7,.2,1); padding: 22px 24px; }
  @keyframes slide { from { opacity: 0; transform: translateX(14px); } }
  .peek .id { color: var(--faint); font-size: 12.5px; }
  .peek h2 { font-size: 22px; font-weight: 600; letter-spacing: -.02em; line-height: 1.2; margin-top: 6px; }
  .props { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
  .prop { background: var(--bg); border: 1px solid var(--line); border-radius: 999px; font-size: 12.5px; padding: 5px 12px; }
  .prop span { color: var(--faint); margin-right: 6px; }
  .peek > p { color: var(--muted); margin-top: 18px; }
  .peek h3 { color: var(--faint); font-size: 11.5px; font-weight: 600; letter-spacing: .08em; margin-top: 26px; text-transform: uppercase; }
  .line { border-left: 1px solid var(--line-2); margin: 12px 0 0 6px; padding-left: 18px; }
  .ev { color: var(--muted); font-size: 13px; padding: 7px 0; position: relative; }
  .ev::before { background: var(--panel); border: 2px solid var(--line-2); border-radius: 50%; content: ''; height: 9px; left: -24px; position: absolute; top: 11px; width: 9px; }
  .ev.hot::before { border-color: var(--accent); }
  .ev b { color: var(--fg); font-weight: 500; }
  .say { align-items: center; background: var(--bg); border: 1px solid var(--line-2); border-radius: calc(var(--r) * .8); color: var(--faint); display: flex; justify-content: space-between; margin: auto 24px 20px; padding: 12px 14px; }
  .status { align-items: center; backdrop-filter: blur(14px); background: color-mix(in srgb, var(--panel) 80%, transparent); border: 1px solid var(--line-2); border-radius: 999px; bottom: 16px; color: var(--muted); display: flex; font-size: 12px; gap: 10px; left: 84px; padding: 7px 14px; position: fixed; }
  .status i { background: var(--ok); border-radius: 50%; box-shadow: 0 0 8px var(--ok); height: 7px; width: 7px; }
  @media (max-width: 900px) { .work { grid-template-columns: 1fr; } .work .pane:last-child, .seg, .stat { display: none; } }
"""

ISSUES = [
    ('In Progress', 'var(--warn)', [
        ('ENG-142', 'Outbox survives a reload', 'eng-142-outbox', 'AL', 'Queued changes are lost if the tab reloads before they are sent. Keep the outbox in the local database and replay it on start.',
         [('hot', '<b>Ada</b> pushed <b>eng-142-outbox</b>', 'moved to In Progress'), ('', '<b>Ada</b> started a timer', '1h 20m so far'), ('', '<b>Kim</b> commented', '“Repro attached.”')]),
        ('ENG-145', 'Batch edits, one request', 'review requested', 'GH', 'Editing forty issues sends forty requests. Send them as one.',
         [('hot', '<b>Grace</b> requested review', 'moved to In Review'), ('', '<b>Grace</b> opened a pull request', 'linked')]),
    ]),
    ('Todo', 'var(--faint)', [
        ('ENG-143', 'Forecast from history', '', 'KJ', 'Replace the estimate field with a forecast built from how long similar issues took.', [('', '<b>Kim</b> created the issue', '2 days ago')]),
        ('ENG-144', 'Notify requesters on ship', '', 'MR', 'When an issue linked to a customer request is done, email the requester.', [('', '<b>Mira</b> linked 3 requests', 'yesterday')]),
        ('ENG-146', 'Lazy-load board columns', '', 'AL', 'Boards with long columns are slow to open. Render what is on screen.', [('', '<b>Ada</b> created the issue', 'today')]),
    ]),
    ('Done', 'var(--ok)', [
        ('ENG-139', 'Signed webhooks', 'merged', 'GH', 'Sign every webhook delivery so receivers can check it came from Cruuze.', [('hot', '<b>Grace</b> merged the pull request', 'moved to Done')]),
    ]),
]


def deck():
    groups, data, first = '', {}, True
    for name, color, items in ISSUES:
        groups += f'<div class="grp"><i style="background:{color}"></i>{name}<span>{len(items)}</span></div>'
        for ident, title, branch, who, text, events in items:
            tag = f'<span class="tag">{ICONS["branch"]}{branch}</span>' if branch else ''
            groups += f'<button class="item" role="option" aria-selected="{str(first).lower()}" data-id="{ident}"><span class="id mono">{ident}</span><span class="t">{title}</span>{tag}<span class="av">{who}</span></button>'
            first = False
            evs = ''.join(f'<div class="ev {k}">{a}<br><span style="color:var(--faint)">{b}</span></div>' for k, a, b in events)
            data[ident] = (
                f'<div class="peek"><span class="id mono">{ident}</span><h2>{title}</h2>'
                f'<div class="props"><span class="prop"><span>Status</span>{name}</span><span class="prop"><span>Assignee</span>{who}</span>'
                f'<span class="prop"><span>Cycle</span>14</span>{f"<span class=prop><span>Branch</span>{branch}</span>" if branch else ""}</div>'
                f'<p>{text}</p><h3>Activity</h3><div class="line">{evs}</div></div>'
                f'<div class="say"><span>Comment…</span><kbd class="mono">⌘⏎</kbd></div>')
    import json
    rail = ''.join(
        f'<button aria-label="{label}"{" aria-current=page" if cur else ""}>{ICONS[key]}{"<b>4</b>" if key == "inbox" else ""}</button>'
        for key, label, cur in [('inbox', 'Inbox', 0), ('focus', 'Focus', 0), ('issues', 'Issues', 1), ('cycles', 'Cycles', 0), ('roadmap', 'Roadmap', 0), ('insights', 'Insights', 0)])
    body = f'''<div class="app">
  <header class="top glass">
    {MARK}
    <button class="ws">Acme <small>/ Engineering</small></button>
    <button class="cmd">{ICONS["search"]}<span>Search, jump or run a command</span><kbd>⌘K</kbd></button>
    <div class="seg" role="group" aria-label="View"><button aria-pressed="true">List</button><button aria-pressed="false">Board</button><button aria-pressed="false">Timeline</button></div>
    <button class="new">{ICONS["plus"]}New issue</button>
  </header>
  <nav class="rail glass" aria-label="Workspace">{rail}<button class="me" aria-label="Account">AL</button></nav>
  <main class="work">
    <section class="pane glass">
      <div class="cycle"><div><h1>Cycle 14</h1><p>6 days left</p></div><div class="meter"><i></i></div>
        <div class="stat"><b>62%</b><span>done</span></div><div class="stat"><b>Oct 16</b><span>85% forecast</span></div></div>
      <div class="scroll" role="listbox" aria-label="Issues">{groups}</div>
    </section>
    <section class="pane glass" id="peek" aria-live="polite"></section>
  </main>
</div>
<div class="status"><i></i>Live<span>·</span>Synced<span>·</span>0 queued</div>'''
    script = f'''<script>
  const PEEKS = {json.dumps(data)}
  const peek = document.getElementById('peek')
  const show = (id) => {{
    peek.innerHTML = PEEKS[id]
    document.querySelectorAll('.item').forEach((el) => el.setAttribute('aria-selected', String(el.dataset.id === id)))
  }}
  document.querySelectorAll('.item').forEach((el) => el.addEventListener('click', () => show(el.dataset.id)))
  document.querySelectorAll('.seg button').forEach((b) => b.addEventListener('click', () => document.querySelectorAll('.seg button').forEach((o) => o.setAttribute('aria-pressed', String(o === b)))))
  show('ENG-142')
</script>'''
    return page('Cruuze app mockup 1: Deck', DECK_CSS, body, script)


# ---------------------------------------------------------------- 2: Flow

FLOW_CSS = """
  .app { display: grid; grid-template-columns: 232px 1fr; height: 100%; }
  aside { border-right: 1px solid var(--line); display: flex; flex-direction: column; gap: 4px; padding: 16px 12px; }
  .brand { align-items: center; display: flex; font-family: var(--display); font-size: 17px; font-weight: 600; gap: 9px; padding: 4px 8px 14px; text-transform: var(--caps); }
  .mark { height: 24px; width: 24px; }
  aside a { text-decoration: none; align-items: center; border-radius: calc(var(--r) * .7); color: var(--muted); display: flex; gap: 11px; padding: 8px 10px; transition: background .2s, color .2s, padding .2s; }
  aside a:hover { background: var(--hover); color: var(--fg); padding-left: 13px; }
  aside a[aria-current] { background: var(--active); color: var(--fg); }
  aside a b { background: var(--accent); border-radius: 99px; color: var(--accent-fg); font-size: 10.5px; margin-left: auto; padding: 1px 7px; }
  aside h2 { color: var(--faint); font-size: 11px; font-weight: 600; letter-spacing: .08em; padding: 18px 10px 6px; text-transform: uppercase; }
  .team i { border-radius: 4px; height: 10px; width: 10px; }
  .user { align-items: center; border: 1px solid var(--line); border-radius: var(--r); display: flex; gap: 10px; margin-top: auto; padding: 10px; }
  .av { align-items: center; background: color-mix(in srgb, var(--accent) 22%, var(--panel)); border-radius: 50%; display: inline-flex; flex: none; font-size: 10px; font-weight: 700; height: 26px; justify-content: center; width: 26px; }
  .user small { color: var(--ok); display: block; font-size: 11.5px; }
  main { display: flex; flex-direction: column; gap: 18px; min-width: 0; overflow-y: auto; padding: 22px 26px 80px; scrollbar-width: thin; }
  .bar { align-items: center; display: flex; gap: 12px; }
  .bar h1 { font-size: 24px; font-weight: 600; letter-spacing: -.02em; }
  .bar p { color: var(--muted); }
  .cmd { align-items: center; background: var(--panel); border: 1px solid var(--line-2); border-radius: 999px; color: var(--faint); display: flex; gap: 10px; height: 38px; margin-left: auto; padding: 0 8px 0 14px; width: 300px; }
  .cmd kbd { background: var(--hover); border-radius: 6px; color: var(--muted); font: 11px 'Geist Mono', ui-monospace, monospace; margin-left: auto; padding: 3px 7px; }
  .new { align-items: center; background: linear-gradient(160deg, var(--accent), var(--accent-2)); border-radius: 999px; box-shadow: 0 8px 20px -10px var(--accent); color: var(--accent-fg); display: flex; font-weight: 600; gap: 6px; height: 38px; padding: 0 16px 0 12px; }
  .now { display: grid; gap: 14px; grid-template-columns: 1.4fr 1fr 1fr; }
  .card { background: var(--panel); border: 1px solid var(--line); border-radius: calc(var(--r) * 1.3); padding: 20px; transition: border-color .25s, transform .3s cubic-bezier(.2,.7,.2,1); }
  .card:hover { border-color: var(--line-2); transform: translateY(-3px); }
  .card > span:first-child { color: var(--faint); font-size: 11.5px; font-weight: 600; letter-spacing: .08em; text-transform: uppercase; }
  .next { background: radial-gradient(ellipse 90% 120% at 0% 0%, var(--glow), transparent 60%), var(--panel); }
  .next h2 { font-size: 22px; font-weight: 600; letter-spacing: -.02em; line-height: 1.2; margin-top: 10px; }
  .next p { color: var(--muted); margin-top: 6px; }
  .acts { display: flex; gap: 8px; margin-top: 18px; }
  .go { align-items: center; background: var(--fg); border-radius: 999px; color: var(--bg); display: flex; font-weight: 600; gap: 7px; height: 36px; padding: 0 16px 0 12px; }
  .go svg { height: 13px; width: 13px; }
  .alt { border: 1px solid var(--line-2); border-radius: 999px; color: var(--muted); height: 36px; padding: 0 14px; }
  .ring { align-items: center; display: flex; gap: 18px; margin-top: 12px; }
  .ring svg { flex: none; height: 92px; transform: rotate(-90deg); width: 92px; }
  .ring circle { fill: none; stroke-width: 9; }
  .ring .bg { stroke: var(--hover); }
  .ring .fg { stroke: var(--accent); stroke-dasharray: 251; stroke-dashoffset: 95; stroke-linecap: round; transition: stroke-dashoffset 1s; }
  .ring b { display: block; font-size: 26px; font-weight: 600; letter-spacing: -.03em; }
  .ring span { color: var(--muted); font-size: 12.5px; }
  .dates { border-top: 1px solid var(--line); color: var(--muted); display: flex; font-size: 12px; justify-content: space-between; margin-top: 14px; padding-top: 12px; }
  .dates b { color: var(--fg); font-weight: 500; }
  .note { align-items: center; border-bottom: 1px solid var(--line); display: flex; font-size: 13px; gap: 10px; padding: 9px 0; }
  .note:last-child { border: 0; }
  .note span { color: var(--faint); font-size: 11.5px; margin-left: auto; white-space: nowrap; }
  .note i { background: var(--accent); border-radius: 50%; flex: none; height: 6px; width: 6px; }
  .board { display: grid; gap: 14px; grid-template-columns: repeat(4, minmax(0, 1fr)); }
  .col { background: color-mix(in srgb, var(--panel) 55%, transparent); border: 1px solid var(--line); border-radius: calc(var(--r) * 1.3); padding: 12px; }
  .col > h3 { align-items: center; display: flex; font-size: 12.5px; font-weight: 600; gap: 8px; padding: 2px 4px 12px; }
  .col > h3 i { border-radius: 50%; height: 8px; width: 8px; }
  .col > h3 span { color: var(--faint); font-family: var(--font); font-weight: 400; }
  .tk { background: var(--panel); border: 1px solid var(--line); border-radius: var(--r); cursor: grab; margin-bottom: 9px; padding: 13px; transition: border-color .2s, box-shadow .25s, transform .25s cubic-bezier(.2,.7,.2,1); }
  .tk:hover { border-color: var(--accent); box-shadow: 0 16px 30px -18px var(--accent); transform: translateY(-3px) rotate(-.6deg); }
  .tk small { color: var(--faint); display: block; font-size: 11.5px; margin-bottom: 4px; }
  .tk footer { align-items: center; display: flex; gap: 6px; margin-top: 12px; }
  .tk footer .av { height: 22px; margin-left: auto; width: 22px; }
  .tag { align-items: center; border: 1px solid var(--line-2); border-radius: 999px; color: var(--muted); display: inline-flex; font-size: 11px; gap: 4px; padding: 1px 8px; }
  .tag svg { height: 11px; width: 11px; }
  .tag.ok { border-color: transparent; background: color-mix(in srgb, var(--ok) 16%, transparent); color: var(--ok); }
  .add { border: 1px dashed var(--line-2); border-radius: var(--r); color: var(--faint); padding: 9px; text-align: center; width: 100%; }
  .add:hover { border-color: var(--accent); color: var(--fg); }
  @media (max-width: 1000px) { .app { grid-template-columns: 1fr; } aside { display: none; } .now { grid-template-columns: 1fr; } .board { grid-template-columns: 1fr 1fr; } .cmd { display: none; } }
"""

BOARD = [
    ('Todo', 'var(--faint)', [('ENG-143', 'Forecast from history', '', 'KJ'), ('ENG-144', 'Notify requesters on ship', '', 'MR'), ('ENG-146', 'Lazy-load board columns', '', 'AL')]),
    ('In Progress', 'var(--warn)', [('ENG-142', 'Outbox survives a reload', 'eng-142-outbox', 'AL'), ('ENG-147', 'Outbox drops edits on reload', '', 'KJ')]),
    ('In Review', 'var(--accent)', [('ENG-145', 'Batch edits, one request', 'review requested', 'GH')]),
    ('Done', 'var(--ok)', [('ENG-139', 'Signed webhooks', 'merged', 'GH'), ('ENG-140', 'Per-person API keys', 'merged', 'MR')]),
]


def flow():
    cols = ''
    for name, color, cards in BOARD:
        inner = ''
        for ident, title, branch, who in cards:
            tag = ''
            if branch:
                tag = f'<span class="tag{" ok" if branch == "merged" else ""}">{ICONS["branch"]}{branch}</span>'
            inner += f'<article class="tk"><small class="mono">{ident}</small>{title}<footer>{tag}<span class="av">{who}</span></footer></article>'
        cols += f'<section class="col"><h3><i style="background:{color}"></i>{name}<span>{len(cards)}</span></h3>{inner}<button class="add">+</button></section>'
    nav = ''.join(
        f'<a href="#"{" aria-current=page" if cur else ""}>{ICONS[key]}{label}{"<b>4</b>" if key == "inbox" else ""}</a>'
        for key, label, cur in [('focus', 'Focus', 1), ('inbox', 'Inbox', 0), ('issues', 'My issues', 0), ('roadmap', 'Roadmap', 0), ('insights', 'Insights', 0)])
    body = f'''<div class="app">
  <aside>
    <div class="brand">{MARK}Cruuze</div>
    {nav}
    <h2>Teams</h2>
    <a class="team" href="#"><i style="background:var(--accent)"></i>Engineering</a>
    <a class="team" href="#"><i style="background:var(--warn)"></i>Design</a>
    <a class="team" href="#"><i style="background:var(--ok)"></i>Support</a>
    <div class="user"><span class="av">AL</span><div>Ada Lovelace<small>● Live</small></div></div>
  </aside>
  <main>
    <div class="bar"><div><h1>Focus</h1><p>Engineering · Cycle 14</p></div>
      <button class="cmd">{ICONS["search"]}<span>Search or run a command</span><kbd>⌘K</kbd></button>
      <button class="new">{ICONS["plus"]}New issue</button></div>
    <div class="now">
      <div class="card next"><span>Up next</span><h2>Outbox survives a reload</h2><p><span class="mono">ENG-142</span> · In Progress · branch pushed 2h ago</p>
        <div class="acts"><button class="go">{ICONS["play"]}Start timer</button><button class="alt">Open</button><button class="alt">Skip</button></div></div>
      <div class="card"><span>Cycle 14</span>
        <div class="ring"><svg viewBox="0 0 100 100"><circle class="bg" cx="50" cy="50" r="40"/><circle class="fg" cx="50" cy="50" r="40"/></svg><div><b>62%</b><span>6 days left</span></div></div>
        <div class="dates"><span>50% <b>Oct 12</b></span><span>85% <b>Oct 16</b></span><span>95% <b>Oct 19</b></span></div></div>
      <div class="card"><span>Inbox</span>
        <div style="margin-top:8px">
          <div class="note"><i></i>Kim mentioned you on ENG-147<span>12m</span></div>
          <div class="note"><i></i>Review requested on ENG-145<span>1h</span></div>
          <div class="note">ENG-139 merged<span>3h</span></div>
          <div class="note">Mira linked 3 requests<span>1d</span></div>
        </div></div>
    </div>
    <div class="board">{cols}</div>
  </main>
</div>'''
    return page('Cruuze app mockup 2: Flow', FLOW_CSS, body)


(HERE / '1-deck.html').write_text(deck())
(HERE / '2-flow.html').write_text(flow())
print('built 1-deck.html and 2-flow.html')
