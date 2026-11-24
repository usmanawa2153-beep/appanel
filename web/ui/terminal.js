"use strict";
/* A small terminal emulator for the web terminal.
   Covers what a shell session needs: colours, cursor movement, line erase,
   readline redraws, clear. It does NOT implement the alternate screen, so
   full-screen programs (vim, htop, nano) are not supported yet.
   All output is written with textContent, never innerHTML. */

const ANSI16 = ["#2b303b", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#abb2bf",
  "#5c6370", "#ff7b86", "#b5e890", "#f5d78e", "#7cc4ff", "#d98cf0", "#6fd2de", "#ffffff"];

function color256(n) {
  if (n < 16) return ANSI16[n];
  if (n < 232) {
    n -= 16;
    const f = (v) => (v ? 55 + v * 40 : 0);
    return `rgb(${f(Math.floor(n / 36))},${f(Math.floor(n / 6) % 6)},${f(n % 6)})`;
  }
  const v = 8 + (n - 232) * 10;
  return `rgb(${v},${v},${v})`;
}

const MAX_LINES = 2000;
const LINE_HEIGHT = 18;

class WebTerminal {
  constructor(host, send) {
    this.host = host;
    this.send = send;
    this.onResize = null;
    this.cols = 80;
    this.rows = 24;
    this.lines = [this.blank()];
    this.els = [];
    this.r = 0;
    this.c = 0;
    this.pen = { fg: null, bg: null, bold: false, dim: false, italic: false, ul: false, inv: false };
    this.styles = new Map([["", this.pen]]);
    this.penKey = "";
    this.dec = new TextDecoder("utf-8");
    this.state = 0; // 0 text, 1 ESC, 2 CSI, 3 OSC, 4 OSC+ESC, 5 skip-one
    this.csi = "";
    this.dirty = new Set();
    this.full = true;
    this.raf = 0;
    this.lastCur = -1;
    this.scroller = host;
    this.probe = null;
    this.bindInput();
  }

  blank() { return { ch: [], st: [] }; }

  /* ---------- sizing ---------- */
  fit() {
    if (!this.probe) {
      this.probe = document.createElement("span");
      this.probe.className = "term-probe";
      this.probe.textContent = "XXXXXXXXXX";
      this.host.append(this.probe);
    }
    const w = this.probe.getBoundingClientRect().width / 10 || 8;
    const cs = getComputedStyle(this.host);
    const innerW = this.host.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const innerH = this.host.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    const cols = Math.max(20, Math.floor(innerW / w) - 1);
    const rows = Math.max(5, Math.floor(innerH / LINE_HEIGHT));
    if (cols !== this.cols || rows !== this.rows) {
      this.cols = cols; this.rows = rows;
      if (this.onResize) this.onResize(cols, rows);
    }
  }

  /* ---------- input ---------- */
  bindInput() {
    const h = this.host;
    h.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "c" && String(getSelection()).length) return; // copy
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "v") return; // paste event handles it
      const s = keyToString(e);
      if (s == null) return;
      e.preventDefault();
      this.type(s);
    });
    h.addEventListener("paste", (e) => {
      e.preventDefault();
      const t = (e.clipboardData && e.clipboardData.getData("text")) || "";
      if (t) this.type(t.replace(/\r?\n/g, "\r"));
    });
    h.addEventListener("click", () => { if (!String(getSelection()).length) h.focus(); });
  }

  type(s) { this.send(new TextEncoder().encode(s)); }

  /* ---------- output ---------- */
  write(bytes) {
    const text = this.dec.decode(bytes, { stream: true });
    for (const ch of text) this.put(ch);
    this.schedule();
  }

  notice(text) { this.write(new TextEncoder().encode("\r\n" + text + "\r\n")); }

  put(ch) {
    const code = ch.codePointAt(0);
    switch (this.state) {
      case 0:
        if (code === 0x1b) this.state = 1;
        else if (code === 0x0a || code === 0x0b || code === 0x0c) this.lineFeed();
        else if (code === 0x0d) this.c = 0;
        else if (code === 0x08) this.c = Math.max(0, this.c - 1);
        else if (code === 0x09) this.c = Math.min(this.cols - 1, (this.c + 8) & ~7);
        else if (code >= 0x20 && code !== 0x7f) this.putChar(ch);
        return;
      case 1:
        if (ch === "[") { this.state = 2; this.csi = ""; }
        else if (ch === "]") this.state = 3;
        else if (ch === "(" || ch === ")" || ch === "#") this.state = 5;
        else this.state = 0; // ESC 7 / 8 / = / > ... ignored
        return;
      case 2:
        if (code >= 0x40 && code <= 0x7e) { this.state = 0; this.runCSI(ch, this.csi); }
        else if (this.csi.length < 64) this.csi += ch;
        return;
      case 3:
        if (code === 0x07) this.state = 0;
        else if (code === 0x1b) this.state = 4;
        return;
      case 4:
        this.state = 0; // ESC \ terminates the OSC string
        return;
      default:
        this.state = 0;
    }
  }

  ensureRow(r) { while (this.lines.length <= r) this.lines.push(this.blank()); }

  lineFeed() {
    this.dirty.add(this.r);
    this.r++;
    this.ensureRow(this.r);
    if (this.lines.length > MAX_LINES) {
      this.lines.shift();
      this.r--;
      this.full = true;
    }
  }

  putChar(ch) {
    if (this.c >= this.cols) { this.c = 0; this.lineFeed(); }
    this.ensureRow(this.r);
    const ln = this.lines[this.r];
    while (ln.ch.length < this.c) { ln.ch.push(" "); ln.st.push(""); }
    ln.ch[this.c] = ch;
    ln.st[this.c] = this.penKey;
    this.c++;
    this.dirty.add(this.r);
  }

  top() { return Math.max(0, this.lines.length - this.rows); }

  runCSI(final, raw) {
    const priv = raw[0] === "?" || raw[0] === ">" || raw[0] === "=";
    if (priv) return; // mode switches (bracketed paste, cursor visibility, alt screen) are ignored
    const p = raw === "" ? [] : raw.split(";").map((x) => (x === "" ? 0 : parseInt(x, 10) || 0));
    const n = Math.max(1, p[0] || 1);
    this.ensureRow(this.r);
    const ln = this.lines[this.r];
    switch (final) {
      case "m": this.sgr(p.length ? p : [0]); break;
      case "A": this.dirty.add(this.r); this.r = Math.max(0, this.r - n); this.dirty.add(this.r); break;
      case "B": this.dirty.add(this.r); this.r += n; this.ensureRow(this.r); this.dirty.add(this.r); break;
      case "C": this.c = Math.min(this.cols - 1, this.c + n); this.dirty.add(this.r); break;
      case "D": this.c = Math.max(0, this.c - n); this.dirty.add(this.r); break;
      case "G": this.c = Math.min(this.cols - 1, n - 1); this.dirty.add(this.r); break;
      case "d": this.dirty.add(this.r); this.r = this.top() + n - 1; this.ensureRow(this.r); this.dirty.add(this.r); break;
      case "H": case "f": {
        this.dirty.add(this.r);
        this.r = this.top() + Math.max(1, p[0] || 1) - 1;
        this.c = Math.min(this.cols - 1, Math.max(1, p[1] || 1) - 1);
        this.ensureRow(this.r);
        this.dirty.add(this.r);
        break;
      }
      case "K": {
        const mode = p[0] || 0;
        if (mode === 0) { ln.ch.length = Math.min(ln.ch.length, this.c); ln.st.length = ln.ch.length; }
        else if (mode === 1) { for (let i = 0; i <= this.c && i < ln.ch.length; i++) { ln.ch[i] = " "; ln.st[i] = ""; } }
        else { ln.ch = []; ln.st = []; }
        this.dirty.add(this.r);
        break;
      }
      case "J": {
        const mode = p[0] || 0;
        if (mode === 2 || mode === 3) {
          this.lines = [this.blank()]; this.r = 0; this.full = true;
        } else if (mode === 0) {
          ln.ch.length = Math.min(ln.ch.length, this.c); ln.st.length = ln.ch.length;
          this.lines.length = this.r + 1; this.full = true;
        } else {
          for (let i = 0; i < this.r; i++) this.lines[i] = this.blank();
          for (let i = 0; i <= this.c && i < ln.ch.length; i++) { ln.ch[i] = " "; ln.st[i] = ""; }
          this.full = true;
        }
        break;
      }
      case "P": ln.ch.splice(this.c, n); ln.st.splice(this.c, n); this.dirty.add(this.r); break;
      case "@": {
        while (ln.ch.length < this.c) { ln.ch.push(" "); ln.st.push(""); }
        ln.ch.splice(this.c, 0, ...Array(n).fill(" "));
        ln.st.splice(this.c, 0, ...Array(n).fill(""));
        this.dirty.add(this.r);
        break;
      }
      case "X": for (let i = this.c; i < this.c + n && i < ln.ch.length; i++) { ln.ch[i] = " "; } this.dirty.add(this.r); break;
      default: break; // everything else is ignored
    }
  }

  sgr(p) {
    const pen = { ...this.pen };
    for (let i = 0; i < p.length; i++) {
      const v = p[i];
      if (v === 0) Object.assign(pen, { fg: null, bg: null, bold: false, dim: false, italic: false, ul: false, inv: false });
      else if (v === 1) pen.bold = true;
      else if (v === 2) pen.dim = true;
      else if (v === 3) pen.italic = true;
      else if (v === 4) pen.ul = true;
      else if (v === 7) pen.inv = true;
      else if (v === 22) { pen.bold = false; pen.dim = false; }
      else if (v === 23) pen.italic = false;
      else if (v === 24) pen.ul = false;
      else if (v === 27) pen.inv = false;
      else if (v >= 30 && v <= 37) pen.fg = ANSI16[v - 30];
      else if (v >= 90 && v <= 97) pen.fg = ANSI16[v - 90 + 8];
      else if (v >= 40 && v <= 47) pen.bg = ANSI16[v - 40];
      else if (v >= 100 && v <= 107) pen.bg = ANSI16[v - 100 + 8];
      else if (v === 39) pen.fg = null;
      else if (v === 49) pen.bg = null;
      else if (v === 38 || v === 48) {
        const key = v === 38 ? "fg" : "bg";
        if (p[i + 1] === 5 && p[i + 2] != null) { pen[key] = color256(p[i + 2] & 255); i += 2; }
        else if (p[i + 1] === 2 && p[i + 4] != null) { pen[key] = `rgb(${p[i + 2] & 255},${p[i + 3] & 255},${p[i + 4] & 255})`; i += 4; }
      }
    }
    this.pen = pen;
    this.penKey = [pen.fg, pen.bg, +pen.bold, +pen.dim, +pen.italic, +pen.ul, +pen.inv].join("|");
    if (this.penKey === "|||0|0|0|0") this.penKey = "";
    if (!this.styles.has(this.penKey)) this.styles.set(this.penKey, pen);
  }

  /* ---------- rendering ---------- */
  schedule() {
    if (!this.raf) this.raf = requestAnimationFrame(() => { this.raf = 0; this.render(); });
  }

  render() {
    const stick = this.scroller.scrollHeight - this.scroller.scrollTop - this.scroller.clientHeight < LINE_HEIGHT * 2;
    if (this.full) {
      for (const el of this.els) el.remove();
      this.els = [];
      for (let i = 0; i < this.lines.length; i++) this.dirty.add(i);
      this.full = false;
    }
    if (this.lastCur >= 0) this.dirty.add(this.lastCur);
    this.dirty.add(this.r);
    while (this.els.length > this.lines.length) this.els.pop().remove();
    // New rows only ever appear at the end, so appending in index order keeps the DOM ordered.
    for (let i = 0; i < this.lines.length; i++) {
      if (this.els[i]) continue;
      const row = this.buildRow(i);
      this.host.append(row);
      this.els[i] = row;
      this.dirty.delete(i);
    }
    for (const i of this.dirty) {
      if (i >= this.lines.length) continue;
      const row = this.buildRow(i);
      this.els[i].replaceWith(row);
      this.els[i] = row;
    }
    this.dirty.clear();
    this.lastCur = this.r;
    if (stick) this.scroller.scrollTop = this.scroller.scrollHeight;
  }

  buildRow(i) {
    const row = document.createElement("div");
    row.className = "term-row";
    const ln = this.lines[i];
    const cur = i === this.r ? this.c : -1;
    const len = Math.max(ln.ch.length, cur + 1);
    let run = "", runKey = null, runCur = false;
    const flush = () => {
      if (!run) return;
      const span = document.createElement("span");
      span.textContent = run;
      if (runCur) span.className = "cur";
      else if (runKey) this.applyStyle(span, this.styles.get(runKey));
      row.append(span);
      run = "";
    };
    for (let x = 0; x < len; x++) {
      const ch = ln.ch[x] === undefined ? " " : ln.ch[x];
      const key = ln.st[x] || "";
      const isCur = x === cur;
      if (isCur !== runCur || key !== runKey) { flush(); runKey = key; runCur = isCur; }
      run += ch;
    }
    flush();
    return row;
  }

  applyStyle(span, pen) {
    if (!pen) return;
    let fg = pen.fg, bg = pen.bg;
    if (pen.inv) { const t = fg; fg = bg || "#0d1017"; bg = t || "#d7dae0"; }
    if (fg) span.style.color = fg;
    if (bg) span.style.backgroundColor = bg;
    if (pen.bold) span.style.fontWeight = "700";
    if (pen.dim) span.style.opacity = "0.65";
    if (pen.italic) span.style.fontStyle = "italic";
    if (pen.ul) span.style.textDecoration = "underline";
  }
}

const KEYS = {
  Enter: "\r", Backspace: "\x7f", Tab: "\t", Escape: "\x1b",
  ArrowUp: "\x1b[A", ArrowDown: "\x1b[B", ArrowRight: "\x1b[C", ArrowLeft: "\x1b[D",
  Home: "\x1b[H", End: "\x1b[F", Delete: "\x1b[3~", Insert: "\x1b[2~", PageUp: "\x1b[5~", PageDown: "\x1b[6~",
};

function keyToString(e) {
  const k = e.key;
  if (e.metaKey) return null;
  if (e.ctrlKey && !e.altKey && k.length === 1) {
    const c = k.toLowerCase().charCodeAt(0);
    if (c >= 97 && c <= 122) return String.fromCharCode(c - 96);
    if (k === "[") return "\x1b";
    if (k === "\\") return "\x1c";
    if (k === "]") return "\x1d";
    if (k === " " || k === "@") return "\0";
    return null;
  }
  if (KEYS[k] !== undefined) return KEYS[k];
  if ([...k].length === 1 && !e.ctrlKey) return e.altKey ? "\x1b" + k : k;
  return null;
}
