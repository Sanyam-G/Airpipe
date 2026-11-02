# Generates airpipe-flow.svg (wide) and airpipe-flow-tall.svg from one timeline.
import sys

T = 15.0          # one shared clock (s); packets loop at 1s, 15 per cycle
ADV = 7.8         # 13px mono advance; typed lines are pinned to it with textLength
PHRASE = "DUSK PINE ECHO GUST 42"
CMD_S = "airpipe send demo.zip"
CMD_R = "airpipe download " + PHRASE
EIO = (.45, 0, .55, 1)  # offer/answer trip easing


def p(t):
    return f"{t / T * 100:.3f}%"


def kf(name, frames):
    """frames: [(t, 'css', timing|None)] -> @keyframes, first frame also fills 0%."""
    out = []
    for i, (t, css, tf) in enumerate(frames):
        sel = p(t)
        if i == 0 and t > 0:
            sel = "0%," + sel
        if i == len(frames) - 1 and t < T:
            sel += ",100%"
        tfs = f";animation-timing-function:{tf}" if tf else ""
        out.append(f"{sel}{{{css}{tfs}}}")
    return f".{name}{{animation-name:{name}}}@keyframes {name}{{{''.join(out)}}}"


def ap(name, t):  # terminal output prints instantly
    return kf(name, [(t, "opacity:0", None), (t + .01, "opacity:1", None)])


def win(t1, t2):  # visible between t1 and t2, hard cuts
    return [(t1, "opacity:0", None), (t1 + .01, "opacity:1", None), (t2, "opacity:1", None), (t2 + .01, "opacity:0", None)]


def bez(x1, y1, x2, y2, x):
    def b(a, c, s):
        return 3 * a * s * (1 - s) ** 2 + 3 * c * s * s * (1 - s) + s ** 3
    lo, hi = 0.0, 1.0
    for _ in range(50):
        m = (lo + hi) / 2
        lo, hi = (m, hi) if b(x1, x2, m) < x else (lo, m)
    return b(y1, y2, lo)


def t_at(frac):  # time fraction at which an EIO trip reaches distance fraction
    lo, hi = 0.0, 1.0
    for _ in range(50):
        m = (lo + hi) / 2
        lo, hi = (m, hi) if bez(*EIO, m) < frac else (lo, m)
    return lo


def build(tall):
    W = 390 if tall else 370          # window width
    LBL = 12 if tall else 11
    if tall:
        VW, VH = 400, 830
        SX, SY, RX, RY = 5, 5, 5, 525
    else:
        VW, VH = 940, 340
        SX, SY, RX, RY = 20, 20, 550, 20
    row = lambda k: 58 + 22 * k
    cx0 = 20                            # content left (local)
    cmdx = cx0 + 2 * ADV                # command starts after "$ "

    css = []
    A = []  # animation css

    # ---------------- windows ----------------
    def window(role, x, y, title, body, frame_cls):
        return f'''<g transform="translate({x} {y})">
<rect class="A {frame_cls}" x=".5" y=".5" width="{W - 1}" height="299" rx="10" fill="#100D0A" stroke="rgba(236,230,220,.12)"/>
<circle cx="18" cy="16" r="5" fill="#2A231D"/><circle cx="34" cy="16" r="5" fill="#2A231D"/><circle cx="50" cy="16" r="5" fill="#2A231D"/>
<text x="{W / 2}" y="20" text-anchor="middle" class="t dim">{title}</text>
<line x1="0" y1="32.5" x2="{W}" y2="32.5" stroke="rgba(236,230,220,.07)"/>
<g class="A live">{body}</g>
</g>'''

    def typed(cid, cmd, cls_clip, cls_caret_move, cls_caret_vis):
        n = len(cmd)
        L = n * ADV
        return f'''<text x="{cx0}" y="{row(0)}" class="dim">$</text>
<clipPath id="{cid}"><rect class="A fb {cls_clip}" x="{cmdx - 1}" y="{row(0) - 15}" width="{L + 1}" height="21"/></clipPath>
<g clip-path="url(#{cid})"><text x="{cmdx}" y="{row(0)}" class="ink" textLength="{L:.1f}" lengthAdjust="spacing">{cmd}</text></g>
<g class="A {cls_caret_vis}"><rect class="A {cls_caret_move}" x="{cmdx}" y="{row(0) - 11}" width="7.4" height="14" fill="#ECE6DC"/></g>'''

    bar_y = row(9) - 8
    sender = typed("tyS", CMD_S, "tyS", "cmS", "cvS") + f'''
<text x="{cx0}" y="{row(1)}" class="A a1" font-weight="700"><tspan class="acc">AirPipe</tspan><tspan class="dim" font-weight="400"> &#183; send</tspan></text>
<text x="{cx0}" y="{row(3)}" class="A a2" xml:space="preserve"><tspan class="ink" font-weight="700">demo.zip</tspan><tspan class="dim">  4.2 MB</tspan></text>
<g class="A pp ctr"><rect x="{cx0 + .5}" y="133.5" width="213" height="39" rx="4" fill="rgba(255,79,0,.08)" stroke="#FF4F00"/>
<text x="{cx0 + 14}" y="157.5" class="acc" font-weight="700" textLength="184.8" lengthAdjust="spacing">{PHRASE}</text></g>
<text x="{cx0}" y="{row(6)}" class="A a3 dim">Waiting for receiver to join...</text>
<text x="{cx0}" y="{row(8)}" class="A a4 ok">&#10003; Receiver joined</text>
<g class="A trk"><rect x="{cx0}" y="{bar_y}" width="{W - 40}" height="8" rx="1" fill="#221C17"/><rect class="A fb prS" x="{cx0}" y="{bar_y}" width="{W - 40}" height="8" rx="1" fill="#FF4F00"/></g>
<text x="{cx0}" y="{row(10)}" class="A a5" xml:space="preserve"><tspan class="ok">&#10003; Sent </tspan><tspan class="ink">demo.zip</tspan><tspan class="dim">  (4.2 MB)</tspan></text>'''

    receiver = typed("tyR", CMD_R, "tyR", "cmR", "cvR") + f'''
<text x="{cx0}" y="{row(1)}" class="A b1" font-weight="700"><tspan class="acc">AirPipe</tspan><tspan class="dim" font-weight="400"> &#183; download</tspan></text>
<g class="A b2"><text x="{cx0}" y="{row(3)}" class="ink">Passphrase: <tspan class="acc">{PHRASE}</tspan></text>
<text x="{cx0}" y="{row(4)}" class="ink">Destination: <tspan font-weight="700">.</tspan></text></g>
<text x="{cx0}" y="{row(5)}" class="A b3 ink">Looking up...</text>
<g class="A b4 dim"><text x="{cx0}" y="{row(5)}">No mailbox transfer; opening direct</text><text x="{cx0}" y="{row(6)}">connection...</text></g>
<g class="A trk"><rect x="{cx0}" y="{bar_y}" width="{W - 40}" height="8" rx="1" fill="#221C17"/><rect class="A fb prR" x="{cx0}" y="{bar_y}" width="{W - 40}" height="8" rx="1" fill="#FF4F00"/></g>
<text x="{cx0}" y="{row(10)}" class="A b5 ok">&#10003; Saved: demo.zip</text>'''

    # ---------------- diagram geometry ----------------
    if tall:
        sig = dict(x1=330, y1=SY + 300, x2=330, y2=RY)          # signal column
        pill = (288, 442, 84, 26)
        capx, capanchor = 278, "end"
        caps_y = [433, 449, 469, 485]
        slab = (318, 340, "end")
        pipe = dict(x1=45, y1=SY + 300, x2=45, y2=RY)
        lock = (45, 366)
        plab = [(69, 362, "start"), (69, 380, "start")]
    else:
        ry = SY + row(6) - 4                                      # relay row = 'Waiting...' row
        sig = dict(x1=SX + W, y1=ry, x2=RX, y2=ry)
        pill = (428, ry - 13, 84, 26)
        capx, capanchor = 470, "middle"
        caps_y = [124, 139, 158, 173]
        slab = (470, 240, "middle")
        py = SY + bar_y + 4                                       # pipe = progress bar row
        pipe = dict(x1=SX + W - 20, y1=py, x2=RX + 20, y2=py)
        lock = (470, py)
        plab = [(470, py - 23, "middle"), (470, py + 30, "middle")]

    px, py0, pw, ph = pill
    pcx, pcy = px + pw / 2, py0 + ph / 2
    vert = tall
    # signal segments: peer edge -> pill edge
    if vert:
        segS = (sig["x1"], sig["y1"], pcx, py0)
        segR = (sig["x2"], sig["y2"], pcx, py0 + ph)
        tripd = sig["y2"] - sig["y1"]
        inside = (py0 - sig["y1"], py0 + ph - sig["y1"])
    else:
        segS = (sig["x1"], sig["y1"], px, pcy)
        segR = (sig["x2"], sig["y2"], px + pw, pcy)
        tripd = sig["x2"] - sig["x1"]
        inside = (px - sig["x1"], px + pw - sig["x1"])

    def seg_clip(cid, seg, cls):
        x1, y1, x2, y2 = seg
        x, y = min(x1, x2), min(y1, y2)
        w, h = abs(x2 - x1) or 8, abs(y2 - y1) or 8
        if vert:
            x -= 4
        else:
            y -= 4
        return f'<clipPath id="{cid}"><rect class="A {cls}" x="{x}" y="{y}" width="{w}" height="{h}"/></clipPath>'

    def dashed(seg, cid, grp):
        x1, y1, x2, y2 = seg
        return f'''<g class="A {grp}"><g clip-path="url(#{cid})"><line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="#ECE6DC" stroke-opacity=".5" stroke-width="1.5" stroke-dasharray="3 4"/></g>
<circle cx="{x1}" cy="{y1}" r="2.5" fill="#8D8477"/></g>'''

    caps = ["pairs peers", "never gets the key", "fallback relays", "ciphertext only"]
    caption = "".join(f'<text x="{capx}" y="{y}" text-anchor="{capanchor}" class="t dim">{c}</text>' for c, y in zip(caps, caps_y))

    dx, dy = (0, tripd) if vert else (tripd, 0)
    lx, ly = lock
    plen = (pipe["y2"] - pipe["y1"]) if vert else (pipe["x2"] - pipe["x1"])
    pk_w, pk_h = (3, 10) if vert else (10, 3)
    pk_x = pipe["x1"] - pk_w / 2 if vert else pipe["x1"]
    pk_y = pipe["y1"] if vert else pipe["y1"] - pk_h / 2
    pk_travel = plen - (pk_h if vert else pk_w)

    diagram = f'''<g class="A live">
{seg_clip("cS", segS, "fb drS" if not vert else "ft drS")}{seg_clip("cR", segR, "fe drR" if not vert else "fbt drR")}
{dashed(segS, "cS", "sgS")}
{dashed(segR, "cR", "sgR")}
<g opacity=".25"><circle class="A d1 tr2" cx="{sig["x1"]}" cy="{sig["y1"]}" r="3" fill="#FF4F00"/><circle class="A d2 tr2" cx="{sig["x2"]}" cy="{sig["y2"]}" r="3" fill="#FF4F00"/></g>
<g opacity=".5"><circle class="A d1 tr1" cx="{sig["x1"]}" cy="{sig["y1"]}" r="4" fill="#FF4F00"/><circle class="A d2 tr1" cx="{sig["x2"]}" cy="{sig["y2"]}" r="4" fill="#FF4F00"/></g>
<circle class="A d1" cx="{sig["x1"]}" cy="{sig["y1"]}" r="5" fill="#FF4F00"/><circle class="A d2" cx="{sig["x2"]}" cy="{sig["y2"]}" r="5" fill="#FF4F00"/>
<rect class="A relay" x="{px}" y="{py0}" width="{pw}" height="{ph}" rx="13" fill="#100D0A" stroke="rgba(236,230,220,.18)"/>
<text x="{pcx}" y="{pcy + 4}" text-anchor="middle" class="t dim caps">RELAY</text>
{caption}
<text x="{slab[0]}" y="{slab[1]}" text-anchor="{slab[2]}" class="A lo t dim">encrypted offer {"&#8595;" if vert else "&#8594;"}</text>
<text x="{slab[0]}" y="{slab[1]}" text-anchor="{slab[2]}" class="A la t dim">{"encrypted answer &#8593;" if vert else "&#8592; encrypted answer"}</text>
<line class="A dl" x1="{pipe["x1"]}" y1="{pipe["y1"]}" x2="{pipe["x2"]}" y2="{pipe["y2"]}" stroke="#FF4F00" stroke-width="2" stroke-dasharray="{plen}"/>
<g class="A pipe">{"".join(f'<rect class="pk k{i}" x="{pk_x}" y="{pk_y}" width="{pk_w}" height="{pk_h}" rx="1.5" fill="#ECE6DC"/>' for i in range(4))}</g>
<g class="A lk ctr"><circle cx="{lx}" cy="{ly}" r="12" fill="#14100D" stroke="#3A3029" stroke-width="1.5"/>
<path class="A shk" d="M{lx - 3.5} {ly - 2}v-2.5a3.5 3.5 0 0 1 7 0v2.5" fill="none" stroke="#FF4F00" stroke-width="1.5"/>
<rect x="{lx - 6}" y="{ly - 2}" width="12" height="8" rx="2" fill="#FF4F00"/><circle cx="{lx}" cy="{ly + 1.5}" r="1.2" fill="#14100D"/></g>
<g class="A pl"><text x="{plab[0][0]}" y="{plab[0][1]}" text-anchor="{plab[0][2]}" class="t ink">direct &#183; WebRTC</text>
<text x="{plab[1][0]}" y="{plab[1][1]}" text-anchor="{plab[1][2]}" class="t dim">end-to-end encrypted</text></g>
</g>'''

    # ---------------- timeline ----------------
    nS, nR1, nR = len(CMD_S), len("airpipe download "), len(CMD_R)
    A += [
        # chrome stays; content fades in with the prompts and out (ease-in) at the end
        kf("live", [(0, "opacity:0", None), (.2, "opacity:1", None), (14.4, "opacity:1", "cubic-bezier(.4,0,1,1)"), (14.95, "opacity:0", None)]),
        # sender types, caret leads the text and leaves when the program prints
        kf("tyS", [(.3, "transform:scaleX(0)", f"steps({nS},end)"), (1.3, "transform:scaleX(1)", None)]),
        kf("cmS", [(.3, "transform:translateX(0)", f"steps({nS},end)"), (1.3, f"transform:translateX({nS * ADV:.1f}px)", None)]),
        kf("cvS", win(0, 1.45)[1:]),
        ap("a1", 1.45), ap("a2", 1.65),
        kf("pp", [(1.9, "opacity:0;transform:scale(.96)", "cubic-bezier(0,0,.3,1)"), (2.15, "opacity:1;transform:scale(1)", None)]),
        ap("a3", 2.35), ap("a4", 6.1), ap("a5", 12.6),
        # receiver: idle blink, then types the command with a beat before the passphrase
        kf("cvR", [(0, "opacity:1", None), (.8, "opacity:1", None), (.81, "opacity:0", None), (1.3, "opacity:0", None), (1.31, "opacity:1", None),
                   (1.9, "opacity:1", None), (1.91, "opacity:0", None), (2.4, "opacity:0", None), (2.41, "opacity:1", None), (4.8, "opacity:1", None), (4.81, "opacity:0", None)]),
        kf("tyR", [(3.2, "transform:scaleX(0)", f"steps({nR1},end)"), (3.77, f"transform:scaleX({nR1 / nR:.4f})", None),
                   (3.92, f"transform:scaleX({nR1 / nR:.4f})", f"steps({nR - nR1},end)"), (4.65, "transform:scaleX(1)", None)]),
        kf("cmR", [(3.2, "transform:translateX(0)", f"steps({nR1},end)"), (3.77, f"transform:translateX({nR1 * ADV:.1f}px)", None),
                   (3.92, f"transform:translateX({nR1 * ADV:.1f}px)", f"steps({nR - nR1},end)"), (4.65, f"transform:translateX({nR * ADV:.1f}px)", None)]),
        ap("b1", 4.8), ap("b2", 5.0), kf("b3", win(5.3, 5.6)), ap("b4", 5.6), ap("b5", 12.8),
        # each peer's line draws toward the relay, then everything signalling fades
        kf("drS", [(2.4, f"transform:scale{'Y' if vert else 'X'}(0)", "cubic-bezier(0,0,.3,1)"), (2.75, f"transform:scale{'Y' if vert else 'X'}(1)", None)]),
        kf("drR", [(5.6, f"transform:scale{'Y' if vert else 'X'}(0)", "cubic-bezier(0,0,.3,1)"), (5.95, f"transform:scale{'Y' if vert else 'X'}(1)", None)]),
        kf("sgS", [(2.4, "opacity:0", None), (2.41, "opacity:1", None), (8.55, "opacity:1", "cubic-bezier(.4,0,1,1)"), (9.05, "opacity:0", None)]),
        kf("sgR", [(5.6, "opacity:0", None), (5.61, "opacity:1", None), (8.55, "opacity:1", "cubic-bezier(.4,0,1,1)"), (9.05, "opacity:0", None)]),
        kf("lo", [(6.25, "opacity:0", None), (6.4, "opacity:1", None), (7.3, "opacity:1", None), (7.45, "opacity:0", None)]),
        kf("la", [(7.4, "opacity:0", None), (7.55, "opacity:1", None), (8.55, "opacity:1", "cubic-bezier(.4,0,1,1)"), (9.05, "opacity:0", None)]),
    ]
    # relay: lights when the sender connects, pulses on the receiver connecting and while forwarding
    DIM, ORG, HOT = "rgba(236,230,220,.18)", "#FF4F00", "#FF8A50"
    f_in, f_out = inside[0] / tripd, inside[1] / tripd
    pulses = [(5.95, 6.35)]
    for t0, back in ((6.35, False), (7.5, True)):
        a, b = (1 - f_out, 1 - f_in) if back else (f_in, f_out)
        pulses.append((t0 + t_at(a) - .05, t0 + t_at(b) + .05))
    rf = [(2.75, f"stroke:{DIM};stroke-width:1px", None), (2.95, f"stroke:{ORG};stroke-width:1px", None)]
    for a, b in pulses:
        m = (a + b) / 2
        rf += [(a, f"stroke:{ORG};stroke-width:1px", None), (m, f"stroke:{HOT};stroke-width:2px", None), (b, f"stroke:{ORG};stroke-width:1px", None)]
    rf += [(8.55, f"stroke:{ORG};stroke-width:1px", "cubic-bezier(.4,0,1,1)"), (9.05, f"stroke:{DIM};stroke-width:1px", None)]
    A.append(kf("relay", rf))
    # offer/answer dots: one eased 1s trip each, 2 trailing copies
    for name, t0, sgn in (("d1", 6.35, 1), ("d2", 7.5, -1)):
        tx = f"translate({dx * sgn}px,{dy * sgn}px)"
        for cls, lag in ((name, 0), (name + "t1", .04), (name + "t2", .08)):
            s = t0 + lag
            A.append(kf(cls, [(s, "opacity:0;transform:translate(0,0)", None),
                              (s + .01, "opacity:1;transform:translate(0,0)", f"cubic-bezier{EIO}"),
                              (s + 1, f"opacity:1;transform:{tx}", None), (s + 1.01, f"opacity:0;transform:{tx}", None)]))
    css_trail = ".d1.tr1{animation-name:d1t1}.d1.tr2{animation-name:d1t2}.d2.tr1{animation-name:d2t1}.d2.tr2{animation-name:d2t2}"
    # receiving terminal flashes when a message lands
    for name, t in (("fR", 7.35), ("fS", 8.5)):
        A.append(kf(name, [(t, "stroke:rgba(236,230,220,.12)", None), (t + .1, "stroke:rgba(236,230,220,.5)", None), (t + .3, "stroke:rgba(236,230,220,.12)", None)]))
    # direct path opens, lock snaps on, bytes flow, bars fill (receiver trails the sender)
    A += [
        kf("dl", [(8.65, f"stroke-dashoffset:{plen}px", "cubic-bezier(.2,.8,.2,1)"), (9.1, "stroke-dashoffset:0px", None)]),
        kf("lk", [(9.0, "opacity:0;transform:scale(.6)", "cubic-bezier(.34,1.56,.64,1)"), (9.4, "opacity:1;transform:scale(1)", None)]),
        kf("shk", [(9.28, "transform:translateY(-2px)", "cubic-bezier(.5,0,1,1)"), (9.4, "transform:translateY(0)", None)]),
        kf("trk", [(8.65, "opacity:0", None), (8.95, "opacity:1", None)]),
        kf("pl", [(9.4, "opacity:0", None), (9.6, "opacity:1", None)]),
        kf("pipe", [(9.3, "opacity:0", None), (9.5, "opacity:1", None), (12.4, "opacity:1", None), (12.6, "opacity:0", None)]),
        kf("prS", [(9.45, "transform:scaleX(0)", None), (12.15, "transform:scaleX(.9)", "cubic-bezier(.25,.25,.3,1)"), (12.45, "transform:scaleX(1)", None)]),
        kf("prR", [(9.6, "transform:scaleX(0)", None), (12.3, "transform:scaleX(.9)", "cubic-bezier(.25,.25,.3,1)"), (12.6, "transform:scaleX(1)", None)]),
    ]
    pk_axis = "Y" if vert else "X"
    style = f'''text{{font-size:13px}}.t{{font-size:{LBL}px;font-weight:600}}.caps{{letter-spacing:.08em}}
.dim{{fill:#8D8477}}.ink{{fill:#ECE6DC}}.acc{{fill:#FF4F00}}.ok{{fill:#6FBF73}}
.fb{{transform-box:fill-box;transform-origin:left center}}.fe{{transform-box:fill-box;transform-origin:right center}}
.ft{{transform-box:fill-box;transform-origin:center top}}.fbt{{transform-box:fill-box;transform-origin:center bottom}}.ctr{{transform-box:fill-box;transform-origin:center}}
/* one shared {T:g}s clock; packets loop at 1s so they stay in phase */
.A{{animation:{T:g}s linear infinite}}
{"".join(A)}{css_trail}
.pk{{animation:pk 1s linear infinite}}.k1{{animation-delay:-.25s}}.k2{{animation-delay:-.5s}}.k3{{animation-delay:-.75s}}
@keyframes pk{{0%{{opacity:0;transform:translate{pk_axis}(0)}}10%,90%{{opacity:1}}100%{{opacity:0;transform:translate{pk_axis}({pk_travel}px)}}}}
@media (prefers-reduced-motion:reduce){{*{{animation-play-state:paused!important;animation-delay:-13.5s!important}}}}'''

    aria = ("AirPipe: the sender runs airpipe send and gets a passphrase; the receiver types it. Both reach the relay only to swap "
            "an encrypted WebRTC offer and answer, then the file streams directly between them, encrypted end to end. The relay "
            "never gets the key, and if a direct path fails it forwards ciphertext only.")
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {VW} {VH}" width="{VW}" height="{VH}" font-family="'JetBrains Mono',Menlo,Consolas,'DejaVu Sans Mono',monospace" role="img" aria-label="{aria}">
<style>
{style}
</style>
<rect width="{VW}" height="{VH}" rx="14" fill="#14100D"/>
{window("s", SX, SY, "sender &#8212; zsh", sender, "fS")}
{window("r", RX, RY, "receiver &#8212; zsh", receiver, "fR")}
{diagram}
</svg>
'''


out = sys.argv[1]
open(f"{out}/airpipe-flow.svg", "w").write(build(False))
open(f"{out}/airpipe-flow-tall.svg", "w").write(build(True))
