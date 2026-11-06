# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "numpy==2.5.4",
#   "pandas==3.1.0",
#   "seaborn==0.13.2",
#   "matplotlib==3.11.2",
# ]
# ///
"""Draw the figures of the json/v2 migration study from the files in data/.

    uv run render.py                          # every figure
    uv run render.py engines                  # one figure
    uv run render.py engines --preview DIR    # also a PNG of each variant

The figures read data/final/stats.json (the output of tools/analyze for
the final paired run), data/final/run.log (the machine and the toolchain),
data/census-pre.tsv (the size of each repository before the migration),
data/timeline.tsv (when each step of the work ran) and data/names.tsv
(the role each repository is labeled by). Every mark is drawn from those
files and every number in a label is computed from them. Each figure is
drawn for a light and a dark page, as <name>.svg and <name>-dark.svg next
to this file.

A figure is a function (data, theme) -> Figure, listed in FIGURES.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from datetime import datetime
from functools import cached_property
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import seaborn as sns  # noqa: E402
from matplotlib.collections import LineCollection, PathCollection  # noqa: E402
from matplotlib.ticker import FixedLocator, NullLocator  # noqa: E402
from matplotlib.transforms import Bbox, blended_transform_factory, offset_copy  # noqa: E402

HERE = Path(__file__).resolve().parent
DATA = HERE.parent / "data"
ALL = "ALL"  # the aggregate over every benchmark in stats.json


@dataclass(frozen=True)
class Theme:
    """The colors of one page background."""

    name: str
    suffix: str
    style: str
    bg: str
    fg: str
    muted: str
    grid: str
    strong: str  # the main series: a dark blue on a light page
    medium: str  # a second series or a plain bar


BLUES = sns.color_palette("Blues", 9).as_hex()
LIGHT = Theme(
    name="light", suffix="", style="white", bg="#ffffff", fg="#1f2328", muted="#59636e", grid="#eaeef2",
    strong=BLUES[7], medium=BLUES[4],
)
# On a dark page the strong series is the pale end of the Blues.
DARK = Theme(
    name="dark", suffix="-dark", style="dark", bg="#0d1117", fg="#e6edf3", muted="#9198a1", grid="#21262d",
    strong=BLUES[2], medium=BLUES[5],
)
THEMES = (LIGHT, DARK)

class Data:
    """The files of data/, each read once, when a figure first asks."""

    def __init__(self, root: Path = DATA) -> None:
        self.root = root

    @cached_property
    def stats(self) -> dict:
        return json.loads((self.root / "final" / "stats.json").read_text())

    @cached_property
    def names(self) -> dict[str, str]:
        t = pd.read_csv(self.root / "names.tsv", sep="\t")
        return dict(zip(t.key, t.role))

    @cached_property
    def census(self) -> pd.DataFrame:
        return pd.read_csv(self.root / "census-pre.tsv", sep="\t")

    @cached_property
    def timeline(self) -> pd.DataFrame:
        t = pd.read_csv(self.root / "timeline.tsv", sep="\t")
        t["start"] = pd.to_datetime(t.start)
        t["end"] = pd.to_datetime(t.end)
        return t

    @cached_property
    def run(self) -> dict:
        """The machine and toolchain of the final run, from its log."""
        lines = (self.root / "final" / "run.log").read_text().splitlines()
        go = next(ln for ln in lines if ln.startswith("go version")).split()[2].removeprefix("go")
        cfg = next(ln for ln in lines if ln.startswith("rounds="))
        rounds = int(cfg.split()[0].removeprefix("rounds="))
        return {"cpu": lines[1].strip(), "cores": int(lines[2]), "go": go, "rounds": rounds}

    def aggs(self, frm: str, to: str, unit: str) -> dict[str, dict]:
        return {a["Module"]: a for a in self.stats["aggregates"]
                if a["From"] == frm and a["To"] == to and a["Unit"] == unit}

    def ratios(self, frm: str, to: str, unit: str) -> np.ndarray:
        out = []
        for r in self.stats["records"]:
            if r["unit"] != unit:
                continue
            for c in r["comparisons"]:
                v = c.get("paired_ratio") or c["ratio"]
                if c["from"] == frm and c["to"] == to and v > 0:
                    out.append(v)
        return np.array(out)

    def label(self, key: str) -> str:
        if key == ALL:
            return "all benchmarks together"
        return self.names.get(key, key.replace("_", "/"))

    @cached_property
    def provenance(self) -> str:
        """One line on what ran, for the caption of every benchmark figure."""
        m = self.aggs("base", "after", "sec/op")
        r = self.run
        return (f"{m[ALL]['N']} benchmarks in {len(m) - 1} Go modules of 29 repositories; each build ran each "
                f"benchmark once per round for {r['rounds']} rounds,")

    @cached_property
    def machine(self) -> str:
        r = self.run
        return f"back to back, on one {r['cpu']} ({r['cores']} cores) with Go {r['go']}."


def apply_theme(th: Theme) -> None:
    sns.set_theme(style=th.style, rc={
        "figure.facecolor": th.bg, "axes.facecolor": th.bg, "savefig.facecolor": th.bg,
        "text.color": th.fg, "axes.labelcolor": th.fg, "axes.edgecolor": th.muted,
        "xtick.color": th.muted, "ytick.color": th.muted,
        "font.family": "DejaVu Sans", "font.size": 10,
        "axes.titlesize": 13, "axes.labelsize": 11,
        "xtick.labelsize": 9.5, "ytick.labelsize": 9.5,
        "xtick.bottom": True, "ytick.left": True,
        "xtick.major.size": 3, "ytick.major.size": 3,
        "xtick.major.width": 0.8, "ytick.major.width": 0.8,
        "svg.fonttype": "path", "svg.hashsalt": "json-v2-figures",
    })


# --------------------------------------------------------------------------
# shared pieces
# --------------------------------------------------------------------------

def header(fig, title: str, sub: str, th: Theme, *, y: float = 0.975) -> None:
    """The figure's title in plain words and, under it, what it holds,
    20 points lower whatever the figure's height."""
    fig.text(0.5, y, title, ha="center", va="top", fontsize=13, fontweight="bold", color=th.fg)
    below = offset_copy(fig.transFigure, fig=fig, y=-20, units="points")
    fig.text(0.5, y, sub, transform=below, ha="center", va="top", fontsize=9, color=th.muted, linespacing=1.4)


def caption(fig, lines: list[str], th: Theme, *, size: float = 8.2) -> None:
    """The provenance under a figure: what ran, so a reader can trust it."""
    fig.text(0.5, 0.012, "\n".join(lines), ha="center", va="bottom", fontsize=size, color=th.muted,
             linespacing=1.45)


def frame(ax, th: Theme) -> None:
    for s in ax.spines.values():
        s.set_color(th.muted)
        s.set_linewidth(0.8)


def times(v: float) -> str:
    """A ratio as a reader reads it: 0.5×, 0.83×, 1×."""
    for exact, name in ((1 / 3, "0.33"), (2 / 3, "0.67")):
        if abs(v - exact) < 1e-9:
            return name + "×"
    return f"{v:.2f}".rstrip("0").rstrip(".") + "×"


RATIO_TICKS = [1 / 16, 1 / 8, 0.25, 1 / 3, 0.4, 0.5, 2 / 3, 0.8, 1, 1.25, 1.5, 2, 3, 4]


def ratio_axis(ax, lo: float, hi: float, th: Theme, *, which: str = "x") -> None:
    """A log axis of ratios from lo to hi, a tick at each round ratio and a
    dashed line at 1."""
    # Keep 1, then the round ratios outward from it that are at least a
    # seventh of the axis apart, so labels never collide.
    gap = np.log(hi / lo) / 7
    ticks = [1.0]
    for t in sorted((t for t in RATIO_TICKS if lo <= t < 1), reverse=True):
        if np.log(ticks[0] / t) >= gap:
            ticks.insert(0, t)
    for t in (t for t in RATIO_TICKS if 1 < t <= hi):
        if np.log(t / ticks[-1]) >= gap:
            ticks.append(t)
    axis = ax.xaxis if which == "x" else ax.yaxis
    (ax.set_xscale if which == "x" else ax.set_yscale)("log")
    axis.set_major_locator(FixedLocator(ticks))
    axis.set_minor_locator(NullLocator())
    axis.set_ticklabels([times(t) for t in ticks])
    (ax.set_xlim if which == "x" else ax.set_ylim)(lo, hi)
    ax.grid(axis=which, color=th.grid, lw=0.7, zorder=0)
    (ax.axvline if which == "x" else ax.axhline)(1, color=th.muted, lw=0.9, ls=(0, (4, 3)), zorder=1)


def span(values) -> tuple[float, float]:
    """A padded log range that holds every value and 1."""
    v = np.asarray([x for x in values if x > 0 and np.isfinite(x)] + [1.0])
    return float(v.min()) / 1.12, float(v.max()) * 1.12


def order(m: dict[str, dict]) -> list[str]:
    """Modules by geometric mean, smallest first, ties by name, with the
    whole set last."""
    keys = sorted((k for k in m if k != ALL), key=lambda k: (m[k]["Geomean"], k))
    return keys + [ALL] if ALL in m else keys


def rows(keys: list[str]) -> np.ndarray:
    """Row positions, top to bottom, with a gap before the whole set."""
    y = np.arange(len(keys), dtype=float)
    if keys and keys[-1] == ALL:
        y[-1] += 0.8
    return y


def row_axis(ax, keys: list[str], ys: np.ndarray, data: Data, th: Theme, *, label: bool = True) -> None:
    """One row per repository; label=False for a panel that shares the
    first panel's rows."""
    if label:
        ax.set_yticks(ys)
        ax.set_yticklabels([data.label(k) for k in keys])
        ax.set_ylim(ys[-1] + 0.8, -0.8)
    else:
        ax.tick_params(axis="y", labelleft=False)
    ax.tick_params(axis="y", length=0)
    if keys[-1] == ALL:
        ax.axhline((ys[-2] + ys[-1]) / 2, color=th.muted, lw=0.6, alpha=0.6)
        if label:
            ax.get_yticklabels()[-1].set_fontweight("bold")


def directions(ax, th: Theme, less: str, more: str) -> None:
    """Which way is better, written over the line at 1."""
    tr = blended_transform_factory(ax.transData, ax.transAxes)
    ax.text(0.97, 1.012, f"← {less}", transform=tr, ha="right", va="bottom", fontsize=8.5, color=th.muted)
    ax.text(1.03, 1.012, f"{more} →", transform=tr, ha="left", va="bottom", fontsize=8.5, color=th.muted)


def ratio_dots(ax, x, ys, th: Theme, *, lo=None, hi=None, size: float = 34) -> None:
    """Ratios as dots with their intervals as lines. The dots are one solid
    blue: the side of the dashed line at 1 says better or worse, and a
    diverging color would fade out exactly where most ratios sit."""
    if lo is not None:
        ax.hlines(ys, lo, hi, color=th.medium, lw=1.6, alpha=0.9, zorder=2)
    ax.scatter(x, ys, s=size, color=th.strong, edgecolors=th.bg, linewidths=0.8, zorder=3)


# --------------------------------------------------------------------------
# engines: the toolchain alone, then the migration, against the classic engine
# --------------------------------------------------------------------------

def engines(data: Data, th: Theme) -> plt.Figure:
    base = data.aggs("classic", "base", "sec/op")
    after = data.aggs("classic", "after", "sec/op")
    keys = order(after)
    ys = rows(keys)
    b = np.array([base[k]["Geomean"] for k in keys])
    a = np.array([after[k]["Geomean"] for k in keys])
    one = np.ones_like(b)

    fig, ax = plt.subplots(figsize=(7.8, 10.6))
    fig.subplots_adjust(left=0.30, right=0.96, top=0.835, bottom=0.13)
    ax.hlines(ys, np.minimum(a, b), np.maximum(a, b), color=th.muted, lw=1.1, alpha=0.6, zorder=2)
    ax.hlines(ys, np.maximum(a, b), one, color=th.muted, lw=0.8, alpha=0.3, zorder=2)
    ax.scatter(one, ys, s=34, facecolors=th.bg, edgecolors=th.muted, linewidths=1.2, zorder=3,
               label="Go 1.27 built with GOEXPERIMENT=nojsonv2: the classic v1 engine, our code unchanged (1×)")
    ax.scatter(b, ys, s=34, color=th.medium, edgecolors=th.bg, linewidths=0.8, zorder=3,
               label="Go 1.27's default build: the v1 API on the new engine, our code unchanged")
    ax.scatter(a, ys, s=34, color=th.strong, edgecolors=th.bg, linewidths=0.8, zorder=3,
               label="Go 1.27's default build after the migration to the v2 API")
    ratio_axis(ax, *span([*a, *b]), th)
    row_axis(ax, keys, ys, data, th)
    directions(ax, th, "faster", "slower")
    frame(ax, th)
    ax.set_xlabel("Time per operation ÷ time on the classic engine (log scale)")
    ax.set_ylabel("Repository")
    fig.legend(loc="upper center", bbox_to_anchor=(0.5, 0.925), ncol=1, frameon=False, fontsize=8.8,
               handletextpad=0.3, labelcolor=th.fg)
    header(fig, "Three builds of the same benchmarks, by repository",
           f"Time per operation relative to the classic engine, which is 1× in every row. Over all {after[ALL]['N']} "
           f"benchmarks:\n{b[-1]:.2f}× with no code change, and {a[-1]:.2f}× after the migration", th)
    caption(fig, [
        data.provenance, data.machine,
        "Each dot: the geometric mean over a repository's benchmarks of its time per operation ÷ the time on "
        "the classic engine.",
        "From the open dot to the light one is what Go 1.27 gave with no code change; from there to the dark one "
        "is the migration.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# per-repo: what the migration changed, by repository
# --------------------------------------------------------------------------

def per_repo(data: Data, th: Theme) -> plt.Figure:
    m = data.aggs("base", "after", "sec/op")
    keys = order(m)
    ys = rows(keys)
    g = np.array([m[k]["Geomean"] for k in keys])
    lo = np.array([m[k]["GeoLo"] for k in keys])
    hi = np.array([m[k]["GeoHi"] for k in keys])

    fig, ax = plt.subplots(figsize=(7.8, 9.8))
    fig.subplots_adjust(left=0.36, right=0.96, top=0.89, bottom=0.13)
    ratio_dots(ax, g, ys, th, lo=lo, hi=hi)
    ratio_axis(ax, *span([*lo, *hi]), th)
    row_axis(ax, keys, ys, data, th)
    directions(ax, th, "faster", "slower")
    frame(ax, th)
    # The number of benchmarks sits between a repository's name and the
    # plot, where the eye passes anyway.
    ax.tick_params(axis="y", pad=40)
    tr = blended_transform_factory(ax.transAxes, ax.transData)
    ax.text(-0.015, -1.2, "Benchmarks", transform=tr, ha="right", va="bottom", fontsize=8.5, color=th.muted)
    for k, y in zip(keys, ys):
        ax.text(-0.015, y, f"{m[k]['N']}", transform=tr, ha="right", va="center", fontsize=9, color=th.muted,
                fontweight="bold" if k == ALL else "normal")
    ax.set_xlabel("Time per operation after the migration ÷ time before (log scale)")
    ax.set_ylabel("Repository")
    header(fig, "What the migration to the v2 API changed, by repository",
           f"Time per operation after ÷ before: {times(round(m[ALL]['Geomean'], 2))} over all "
           f"{m[ALL]['N']} benchmarks, {1 - m[ALL]['Geomean']:.0%} less time", th)
    caption(fig, [
        data.provenance, data.machine,
        "Dot: the geometric mean over the repository's benchmarks. Line: its 95% bootstrap interval over those "
        "benchmarks.",
        "Both builds run on Go 1.27's default engine; the only difference is the migration to the v2 API.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# memory: bytes and allocations per operation, by repository
# --------------------------------------------------------------------------

def memory(data: Data, th: Theme) -> plt.Figure:
    by = data.aggs("base", "after", "B/op")
    al = data.aggs("base", "after", "allocs/op")
    keys = order(al)
    ys = rows(keys)
    panels = [(by, "Bytes allocated per operation"), (al, "Allocations per operation")]
    vals = [v for m, _ in panels for k in keys for v in (m[k]["GeoLo"], m[k]["GeoHi"])]
    lo, hi = span(vals)

    fig, axes = plt.subplots(1, 2, figsize=(9.6, 9.8), sharey=True)
    fig.subplots_adjust(left=0.245, right=0.975, top=0.885, bottom=0.13, wspace=0.06)
    for ax, (m, title) in zip(axes, panels):
        g = np.array([m[k]["Geomean"] for k in keys])
        ratio_dots(ax, g, ys, th, lo=[m[k]["GeoLo"] for k in keys], hi=[m[k]["GeoHi"] for k in keys])
        ratio_axis(ax, lo, hi, th)
        row_axis(ax, keys, ys, data, th, label=ax is axes[0])
        frame(ax, th)
        ax.set_title(title, fontsize=11, fontweight="bold", color=th.fg, pad=20)
        directions(ax, th, "less", "more")
        ax.set_xlabel("After the migration ÷ before (log scale)")
    axes[0].set_ylabel("Repository")
    header(fig, "What the migration to the v2 API did to memory, by repository",
           f"Over all {by[ALL]['N']} benchmarks: {times(round(by[ALL]['Geomean'], 2))} the bytes and "
           f"{times(round(al[ALL]['Geomean'], 2))} the allocations per operation", th)
    caption(fig, [
        data.provenance, data.machine,
        "Dot: the geometric mean over the repository's benchmarks. Line: its 95% bootstrap interval over those "
        "benchmarks.",
        "A benchmark that allocates nothing before and after counts as 1×.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# distribution: every benchmark's ratio, for time, bytes and allocations
# --------------------------------------------------------------------------

def distribution(data: Data, th: Theme) -> plt.Figure:
    units = [("sec/op", "Time per operation", "faster", "slower"),
             ("B/op", "Bytes per operation", "fewer bytes", "more"),
             ("allocs/op", "Allocations per operation", "fewer allocations", "more")]
    width = 0.125  # bins in log2 units, about 9% each
    rs = {u: data.ratios("base", "after", u) for u, *_ in units}
    every = np.concatenate(list(rs.values()))
    lo = max(np.floor(np.log2(every.min())), -3)
    hi = min(max(np.ceil(np.log2(every.max())), 1), 2)
    edges = np.arange(lo, hi + width / 2, width)

    fig, axes = plt.subplots(1, 3, figsize=(12, 4.9), sharey=True)
    fig.subplots_adjust(left=0.06, right=0.99, top=0.73, bottom=0.27, wspace=0.08)
    top = 0
    for ax, (u, title, less, more) in zip(axes, units):
        r = rs[u]
        n, _ = np.histogram(np.clip(np.log2(r), lo, hi - 1e-9), bins=edges)
        top = max(top, n.max())
        ax.bar(2.0 ** edges[:-1], n, width=2.0 ** edges[1:] - 2.0 ** edges[:-1], align="edge",
               color=th.medium, edgecolor=th.bg, linewidth=0.6, zorder=2)
        ax.set_xscale("log", base=2)
        ks = np.arange(lo + 1, hi)  # interior ticks, so neighbors' labels never meet
        ax.xaxis.set_major_locator(FixedLocator(2.0 ** ks))
        ax.xaxis.set_minor_locator(NullLocator())
        ax.set_xticklabels([f"1/{2 ** -k:g}×" if k < 0 else f"{2 ** k:g}×" for k in ks])
        ax.set_xlim(2.0 ** lo, 2.0 ** hi)
        ax.axvline(1, color=th.muted, lw=0.9, ls=(0, (4, 3)), zorder=3)
        ax.grid(axis="y", color=th.grid, lw=0.7, zorder=0)
        frame(ax, th)
        better, worse = int((r < 0.99).sum()), int((r > 1.01).sum())
        ax.set_title(title, fontsize=11, fontweight="bold", color=th.fg, pad=22)
        ax.text(0.5, 1.035, f"{better} {less}, {worse} {more}, {len(r) - better - worse} within 1%",
                transform=ax.transAxes, ha="center", va="bottom", fontsize=8.8, color=th.muted)
        ax.set_xlabel("After the migration ÷ before (log scale)")
    axes[0].set_ylim(0, top * 1.08)
    axes[0].set_ylabel("Number of benchmarks")
    header(fig, "Every benchmark's change after the migration to the v2 API",
           "Each bar counts the benchmarks whose ratio falls in its range, about 9% wide; the outermost bars also "
           "hold everything beyond them", th)
    caption(fig, [
        data.provenance + " " + data.machine,
        "Ratio: the median over the rounds of a benchmark's value after the migration ÷ its value before, in "
        "the same round.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# size: lines of Go and files importing encoding/json, by repository
# --------------------------------------------------------------------------

def compact(v: float) -> str:
    return f"{v / 1000:.0f}K" if v >= 1000 else f"{v:.0f}"


def size(data: Data, th: Theme) -> plt.Figure:
    c = data.census[data.census.v1_files > 0].sort_values(["go_lines", "repo"], ascending=[False, True])
    keys = list(c.repo)
    ys = np.arange(len(keys), dtype=float)

    fig, (ax, bx) = plt.subplots(1, 2, figsize=(9.6, 9.2), sharey=True, width_ratios=[2.3, 1])
    fig.subplots_adjust(left=0.23, right=0.975, top=0.885, bottom=0.115, wspace=0.08)
    for axis, col, name, fmt in ((ax, "go_lines", "Lines of Go, tests included", compact),
                                 (bx, "v1_files", "Files importing encoding/json", lambda v: f"{v:,.0f}")):
        v = c[col].to_numpy()
        axis.barh(ys, v, height=0.68, color=th.medium, edgecolor="none", zorder=2)
        for y, x in zip(ys, v):
            axis.text(x, y, " " + fmt(x), ha="left", va="center", fontsize=8.5, color=th.fg)
        axis.set_xlim(0, v.max() * 1.2)
        axis.set_xlabel(name)
        frame(axis, th)
    ax.xaxis.set_major_formatter(lambda v, _: compact(v))
    row_axis(ax, keys, ys, data, th)
    ax.set_ylabel("Repository")
    header(fig, "Where the JSON code was before the migration",
           f"{len(keys)} repositories: {c.go_lines.sum():,} lines of Go, and {c.v1_files.sum():,} files that "
           f"imported encoding/json", th)
    caption(fig, [
        "Counted at each repository's commit before any benchmark was added: every .go file, tests included,",
        "and the files among them with an encoding/json import. Repositories are named by what they are.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# timeline: when each step ran
# --------------------------------------------------------------------------

def hm(minutes: float) -> str:
    h, m = divmod(round(minutes), 60)
    return f"{m}m" if h == 0 else (f"{h}h" if m == 0 else f"{h}h {m:02d}m")


def timeline(data: Data, th: Theme) -> plt.Figure:
    t = data.timeline
    t0, t1 = t.start.min(), t.end.max()
    s = (t.start - t0).dt.total_seconds().to_numpy() / 3600
    d = (t.end - t.start).dt.total_seconds().to_numpy() / 3600
    ys = np.arange(len(t), dtype=float)
    total = (t1 - t0).total_seconds() / 3600

    fig, ax = plt.subplots(figsize=(9.4, 6.6))
    fig.subplots_adjust(left=0.33, right=0.97, top=0.86, bottom=0.18)
    ax.barh(ys, np.maximum(d, 0.04), left=s, height=0.62, color=th.medium, edgecolor="none", zorder=2)
    # Each duration is written at the end of its bar, where the eye already
    # is; the chart has no vertical grid, so no line runs through a label.
    for y, x, w in zip(ys, s, d):
        ax.text(x + max(w, 0.04), y, " " + hm(w * 60), ha="left", va="center", fontsize=9, color=th.fg)
    ax.set_yticks(ys)
    ax.set_yticklabels(t.label)
    ax.set_ylim(ys[-1] + 0.8, -0.8)
    ax.tick_params(axis="y", length=0)
    ax.set_xlim(0, np.ceil(total) + 0.6)
    ax.xaxis.set_major_locator(FixedLocator(np.arange(0, np.ceil(total) + 1)))
    ax.xaxis.set_major_formatter(lambda v, _: f"{v:g}h")
    frame(ax, th)
    start = datetime.fromisoformat(str(t0))
    ax.set_xlabel(f"Hours since the migration began ({start:%H:%M} UTC, {start:%B} {start.day}, {start.year})")
    ax.set_ylabel("Step")
    header(fig, "Timeline of the migration",
           f"{hm(total * 60)} of wall-clock time from the first survey to the final measurement", th)
    caption(fig, [
        "One Claude Code session ran every step on one machine; an engineer decided what to keep of v1's output.",
        "Where a step names agents, the session started that many Claude Sonnet agents, working in parallel.",
        "Steps overlap where the first repositories were checked and merged while the largest were still migrating.",
    ], th)
    return fig


def overlaps(fig: plt.Figure) -> list[str]:
    """Every place where a text touches a line, a dot, a bar or another
    text, measured on the drawn figure. A figure ships only with none."""
    fig.canvas.draw()
    r = fig.canvas.get_renderer()
    # Matplotlib keeps labels for ticks outside an axis's limits but does
    # not draw them; only the drawn ones count.
    ticks, drawn = set(), set()
    for ax in fig.axes:
        for axis in (ax.xaxis, ax.yaxis):
            for tk in [*axis.majorTicks, *axis.minorTicks]:
                ticks |= {id(tk.label1), id(tk.label2)}
            for tk in axis._update_ticks():
                drawn |= {id(tk.label1), id(tk.label2)}
    texts = []
    for t in fig.findobj(matplotlib.text.Text):
        if id(t) in ticks and id(t) not in drawn:
            continue
        if t.get_visible() and t.get_text().strip() and t.figure is fig:
            bb = t.get_window_extent(r)
            if bb.width > 2 and bb.height > 2:
                texts.append((t.get_text().replace("\n", " ")[:40], Bbox.from_extents(bb.x0 + 1, bb.y0 + 1,
                                                                                   bb.x1 - 1, bb.y1 - 1)))
    marks = []  # (kind, path in display space or None, box, clip box)
    for ax in fig.axes:
        clip = ax.get_window_extent(r)
        for ln in [*ax.lines, *ax.get_xgridlines(), *ax.get_ygridlines()]:
            if ln.get_visible() and len(ln.get_xydata()):
                marks.append(("line", ln.get_transform().transform_path(ln.get_path()), None, clip))
        for c in ax.collections:
            if isinstance(c, LineCollection):
                for path in c.get_paths():
                    marks.append(("line", c.get_transform().transform_path(path), None, clip))
            elif isinstance(c, PathCollection):
                pts = c.get_offset_transform().transform(c.get_offsets())
                sizes = c.get_sizes()
                for i, (x, y) in enumerate(pts):
                    rad = np.sqrt(sizes[i % len(sizes)]) / 2 * fig.dpi / 72 + 1
                    marks.append(("dot", None, Bbox.from_extents(x - rad, y - rad, x + rad, y + rad), None))
        for patch in ax.patches:
            marks.append(("bar", None, patch.get_window_extent(r), None))
        for sp in ax.spines.values():
            if sp.get_visible():
                marks.append(("frame", sp.get_transform().transform_path(sp.get_path()), None, None))
    found = []
    for name, tb in texts:
        for kind, path, box, clip in marks:
            if path is not None:
                area = tb if clip is None else Bbox.intersection(tb, clip)
                if area is not None and area.width > 0 and area.height > 0 and path.intersects_bbox(area, filled=False):
                    found.append(f"text {name!r} crosses a {kind}")
                    break
            elif box.overlaps(tb):
                found.append(f"text {name!r} covers a {kind}")
                break
    for i, (a, ab) in enumerate(texts):
        for b, bb in texts[i + 1:]:
            if ab.overlaps(bb):
                found.append(f"text {a!r} overlaps text {b!r}")
    return found


FIGURES = {
    "engines": engines,
    "per-repo": per_repo,
    "memory": memory,
    "distribution": distribution,
    "size": size,
    "timeline": timeline,
}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("names", nargs="*", metavar="name",
                    help=f"figures to draw, of {', '.join(FIGURES)}; all when none is named")
    ap.add_argument("--preview", type=Path, help="also write a PNG of each variant into this directory")
    args = ap.parse_args()
    if unknown := [n for n in args.names if n not in FIGURES]:
        ap.error(f"no figure named {', '.join(unknown)}")
    data = Data()
    problems = []
    for name in args.names or FIGURES:
        for th in THEMES:
            apply_theme(th)
            fig = FIGURES[name](data, th)
            problems += [f"{name}{th.suffix}: {p}" for p in overlaps(fig)]
            out = HERE / f"{name}{th.suffix}.svg"
            fig.savefig(out, metadata={"Date": None})
            print(f"{out.name}: {out.stat().st_size / 1024:.0f} KiB")
            if args.preview:
                args.preview.mkdir(parents=True, exist_ok=True)
                fig.savefig(args.preview / f"{name}-preview-{th.name}.png", dpi=170)
            plt.close(fig)
    if problems:
        raise SystemExit("overlaps:\n" + "\n".join(problems))


if __name__ == "__main__":
    main()
