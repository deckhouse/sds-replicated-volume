# Layout Quorum Analysis

This document verifies the quorum behavior of each of the 7 canonical layouts
under all relevant failure combinations. For each layout, we show what happens
when nodes fail, whether IO continues, and whether data is safe.

For layout definitions, formulas (D, q, qmr, TB), and the summary table, see
[README.md](README.md) §4–§5. For DRBD quorum mechanics — peer
classification, the quorum condition, and tiebreaker logic — see
[DRBD_MODEL.md](DRBD_MODEL.md). For replica replacement and layout transition
procedures, see [LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 1. 1D (GMDR=0, FTT=0)

**Configuration**: 1 Diskful node. Total: **1 node**. q=1, qmr=1.

| Scenario | up_to_date | voters | q=1 | qmr=1 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK | 1 | 1 | ✓ | ✓ | **Available** |
| 1D fails | 0 | 1 | ✗ | ✗ | **Data loss + unavailable** |

No redundancy. A single permanent disk loss means data loss. Suitable only
for ephemeral data or volumes replicated by other means.

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 2. 2D+1TB (GMDR=0, FTT=1)

**Configuration**: 2 Diskful + 1 TieBreaker. Total: **3 nodes**. q=2, qmr=1.

### Failure scenarios

| Scenario | up_to_date | diskless | q=2 main | Tiebreaker? | Result |
|----------|-----------|----------|----------|-------------|--------|
| All OK | 2 | 1 | 2≥2 ✓ | — | **Available** |
| 1D fails | 1 | 1 | 1<2 ✗ | even ✓, 1=2−1 ✓, qmr(1≥1) ✓, TB(1≥1) ✓, sticky ✓ | **Available** |
| 1TB fails | 2 | 0 | 2≥2 ✓ | — | **Available** |
| 1D + 1TB | 1 | 0 | 1<2 ✗ | TB: 0<1 ✗ | **Unavailable** |
| 2D fail | 0 | 1 | ✗ | ✗ | **Data loss + unavailable** |

Tolerates any single failure for availability. In degraded mode (1D down),
writes go to only 1 disk via Protocol C. Permanent loss of that disk means
data loss (GMDR=0).

### Network partition safety

| Partition | Side A | Side B |
|-----------|--------|--------|
| (1D + 1TB) vs (1D) | TB saves: **Available** | No TB: **Unavailable** |
| (2D) vs (1TB) | 2≥2 ✓: **Available** | No data |

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 3. 2D (GMDR=1, FTT=0)

**Configuration**: 2 Diskful nodes. Total: **2 nodes**. q=2, qmr=2.

### Failure scenarios

| Scenario | up_to_date | voters | q=2 | qmr=2 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK | 2 | 2 | ✓ | ✓ | **Available** |
| 1D fails | 1 | 2 | ✗ | ✗ | **Unavailable** (data safe — 1 copy remains) |

Both nodes must be present for IO. Losing either stops IO immediately. Since
both always have all data (`qmr=2`, Protocol C), 1 permanent disk loss is
survivable — the other copy has everything. GMDR=1.

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 4. 3D (GMDR=1, FTT=1)

**Configuration**: 3 Diskful nodes. Total: **3 nodes**. q=2, qmr=2.

### Failure scenarios

| Scenario | up_to_date | voters | q=2 | qmr=2 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK | 3 | 3 | 3≥2 ✓ | 3≥2 ✓ | **Available** |
| 1D fails | 2 | 3 | 2≥2 ✓ | 2≥2 ✓ | **Available** |
| 2D fail | 1 | 3 | 1<2 ✗ | — | **Unavailable** (data safe — 1 copy) |

In degraded mode (1D down), IO continues with 2 UpToDate nodes (`qmr=2`
met). Writes go to 2 disks via Protocol C. Permanent loss of 1 of those
2 disks leaves the other with all data. Permanent loss of both → data lost.
GMDR=1.

### Network partition safety

| Partition | Side A (2D) | Side B (1D) |
|-----------|-------------|-------------|
| 2D vs 1D | 2≥2 ✓: **Available** | 1<2 ✗: **Unavailable** |

Odd voters (3) — strict majority always resolves partitions unambiguously.

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 5. 4D+1TB (GMDR=1, FTT=2)

**Configuration**: 4 Diskful + 1 TieBreaker. Total: **5 nodes**. q=3, qmr=2.

### Failure scenarios

| Scenario | up_to_date | diskless | q=3 main | Tiebreaker? | Result |
|----------|-----------|----------|----------|-------------|--------|
| All OK | 4 | 1 | 4≥3 ✓ | — | **Available** |
| 1D fails | 3 | 1 | 3≥3 ✓ | — | **Available** |
| 2D fail | 2 | 1 | 2<3 ✗ | even ✓, 2=3−1 ✓, qmr(2≥2) ✓, TB(1≥1) ✓, sticky ✓ | **Available** |
| 1D + 1TB | 3 | 0 | 3≥3 ✓ | — | **Available** |
| 2D + 1TB | 2 | 0 | 2<3 ✗ | no TB | **Unavailable** |
| 3D fail | 1 | 1 | 1<3 ✗ | 1≠3−1=2 | **Unavailable** |

All 2-failure combinations:

| 2 failures | Result |
|------------|--------|
| 2D | ✓ (tiebreaker) |
| 1D + 1TB | ✓ (main: 3≥3) |

After losing 2D: 2 UpToDate remain (≥ `qmr=2`). Writes go to 2 disks.
1 more permanent disk loss leaves 1 copy — data safe. GMDR=1.

### Network partition safety

| Partition | Side A | Side B |
|-----------|--------|--------|
| (2D + 1TB) vs (2D) | TB saves: **Available** | No TB: **Unavailable** |
| (3D) vs (1D + 1TB) | 3≥3 ✓: **Available** | 1<3 ✗: **Unavailable** |
| (3D + 1TB) vs (1D) | 3≥3 ✓: **Available** | 1<3 ✗: **Unavailable** |

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 6. 4D (GMDR=2, FTT=1)

**Configuration**: 4 Diskful nodes. Total: **4 nodes**. q=3, qmr=3.

### Failure scenarios

| Scenario | up_to_date | voters | q=3 | qmr=3 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK | 4 | 4 | 4≥3 ✓ | 4≥3 ✓ | **Available** |
| 1D fails | 3 | 4 | 3≥3 ✓ | 3≥3 ✓ | **Available** |
| 2D fail | 2 | 4 | 2<3 ✗ | 2<3 ✗ | **Unavailable** (data safe) |

In degraded mode (1D down), IO continues with 3 UpToDate nodes (`qmr=3`
met). Writes go to 3 disks. Permanent loss of 2 of those 3 leaves 1 copy.
GMDR=2.

**Why tiebreaker does NOT help here**: adding a TB to this layout would
not help with 2D failure, because the tiebreaker requires `up_to_date >= qmr`
(condition 3 in [DRBD_MODEL.md](DRBD_MODEL.md) §3). With 2D down,
`up_to_date = 2 < qmr = 3` — the tiebreaker condition fails regardless of TB
presence.

### Network partition safety

| Partition | Side A | Side B |
|-----------|--------|--------|
| 3D vs 1D | 3≥3 ✓: **Available** | 1<3 ✗: **Unavailable** |
| 2D vs 2D | 2<3 ✗: **Unavailable** | 2<3 ✗: **Unavailable** |

The 2-2 partition makes both sides unavailable. This is safe (no split-brain),
but means an even network split takes down the entire volume. This is a
2-failure scenario (each side sees 2 missing nodes), exceeding FTT=1.

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 7. 5D (GMDR=2, FTT=2)

**Configuration**: 5 Diskful nodes. Total: **5 nodes**. q=3, qmr=3.

### Failure scenarios

| Scenario | up_to_date | voters | q=3 | qmr=3 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK | 5 | 5 | 5≥3 ✓ | 5≥3 ✓ | **Available** |
| 1D fails | 4 | 5 | 4≥3 ✓ | 4≥3 ✓ | **Available** |
| 2D fail | 3 | 5 | 3≥3 ✓ | 3≥3 ✓ | **Available** |
| 3D fail | 2 | 5 | 2<3 ✗ | 2<3 ✗ | **Unavailable** (data safe) |

After losing 2D: 3 UpToDate remain (≥ `qmr=3`). Writes go to 3 disks.
Permanent loss of 2 of those 3 leaves 1 copy. GMDR=2.

No tiebreaker needed: 5 voters is odd — strict majority (3) always resolves
partitions unambiguously.

### Network partition safety

| Partition | Side A | Side B |
|-----------|--------|--------|
| 3D vs 2D | 3≥3 ✓: **Available** | 2<3 ✗: **Unavailable** |

For replacement procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 8. GMDR / FTT Trade-off by Layout

For recommendations by use case and the GMDR/FTT trade-off explanation, see
[README.md](README.md) §5. The table below maps each trade-off category to
the specific layouts analyzed above:

| Category | Layouts | Behavior under failure |
|----------|---------|----------------------|
| GMDR > FTT | 2D (§3), 4D (§6) | Stops IO on first failure — strict data safety |
| GMDR = FTT | 1D (§1), 3D (§4), 5D (§7) | Available until the threshold — balanced |
| GMDR < FTT | 2D+1TB (§2), 4D+1TB (§5) | Stays available at reduced redundancy — availability-first |
