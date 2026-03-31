# Key datamesh test cases

This document lists the key behavioral cases that must be reviewed and used as the basis for tests.
Cases are grouped by transition group and test scope.
Non-obvious / non-trivial cases are marked with ⚡.

Plan behavior tests (sections 1–19) exercise transitions through `ProcessTransitions`.
Unit tests (sections 20–25) test individual functions directly.
Integration tests (sections 26–31) verify cross-group interactions and invariants
across all canonical layouts, topologies, and feature variants.

---

## 1. AddReplica(A)

- **Dispatch: creates Access member with correct fields.**
  1D existing. Join(A) request for rv-1-1. ProcessTransitions creates Access member with type=Access, nodeName from RVR, addresses cloned from RVR, Attached=false. Revision incremented. Transition has type=AddReplica, planID=access/v1. Membership message written back to request.

- **Zone extracted from RSP eligible node.**
  RSP has node-2 in zone-b. New Access member gets Zone=zone-b.

- **Guard: RV deleting → blocked.**
  RV has DeletionTimestamp. Join(A) request present. No transition created. Request message contains "is terminating".

- **Guard: VolumeAccess=Local → blocked.**
  Configuration has VolumeAccess=Local. No transition created. Request message contains "volumeAccess is Local".

- **Guard: addresses empty → blocked.**
  RVR exists but has no addresses (mkRVRBare). No transition created. Request message mentions "addresses".

- ⚡ **Guard: RVR nil (only request, no RVR) → blocked by node eligibility.**
  Join request exists but no RVR created yet. guardNodeEligible blocks because no node name is known without RVR. Request message contains "not in eligible nodes".

- **Guard: member already on same node → blocked.**
  Existing Diskful member on node-2. New Join(A) request for rv-1-1 also on node-2. Blocked with message naming the existing member type and node.

- **Guard: RSP nil → blocked.**
  No RSP provided. Blocked with "ReplicatedStoragePool" message.

- **Guard: node not in eligible nodes → blocked.**
  RSP has node-1 but not node-2. Join for rv-1-1 on node-2 blocked with "eligible" message.

- ⚡ **Plan selection: already a member → skip.**
  rv-1-1 is already an Access member and has a Join request (transient: request not yet cleaned up). planAddReplica returns skip — no message written, changed=false.

- **Settle: all confirmed → completed.**
  Active AddReplica(A) transition at rev 6. FM (Diskful) + subject (Access) both at rev 6. Transition completed. Message = "Joined datamesh successfully".

- **Settle: partial confirmation → progress message.**
  FM confirmed (rev 6), subject at rev 5 (not confirmed). Transition stays. Progress message contains "1/2" and "revision 6".

- ⚡ **Settle: PendingDatameshJoin not shown as error.**
  Subject RVR has DRBDConfigured=False with reason=PendingDatameshJoin (expected during join). Progress message does NOT contain "Errors" or "PendingDatameshJoin".

- **Settle: other DRBDConfigured errors surfaced.**
  Subject RVR has DRBDConfigured=False with reason=ConfigurationFailed. Progress message contains "Errors" and "ConfigurationFailed".

- ⚡ **Settle: ShadowDiskful in mustConfirm set.**
  3 members: D + A (subject) + sD. AddReplica(A) uses confirmFMPlusSubject: FM includes sD (ConnectsToAllPeers). sD at rev 5 (not confirmed). Transition NOT completed. Progress shows "#2" as waiting.

- ⚡ **Settle: subject RVR disappears during active transition.**
  Active AddReplica(A) for rv-1-1. FM confirmed. But subject RVR is gone (not in rvrs list). Transition NOT completed — subject cannot confirm. Progress shows "1/2".

- **RemoveReplica(A) partial progress.**
  Active RemoveReplica(A). Neither FM nor subject confirmed. Progress shows "0/2".

- ⚡ **Integration: settle + dispatch in same call.**
  Existing AddReplica(rv-1-1) at rev 6 — all confirmed → will complete. Pending Join for rv-1-2. In one ProcessTransitions call: old transition completes, new transition for rv-1-2 created. rv-1-2 added as member. Revision was 6, completion doesn't increment, new join increments to 7.

- **Integration: multiple parallel NonVoting joins.**
  Two Join(A) requests for rv-1-1 and rv-1-2. Both dispatched in parallel (NonVoting can overlap). Each gets its own revision (6 and 7). Two transitions created.

- ⚡ **Integration: active same-type skips dispatch.**
  AddReplica(A) active for rv-1-1 (not yet confirmed). Join request for rv-1-1 still present. Dispatcher yields DispatchReplica, engine sees same-type on slot → silent ignore. Still exactly one transition.

- ⚡ **Integration: RemoveReplica blocks AddReplica for same replica.**
  Active RemoveReplica(A) for rv-1-1 (not confirmed). Join request for rv-1-1 (re-join after leave). Engine slot conflict: RemoveReplica occupies membership slot, AddReplica for same replica blocked (different type on same slot). Only RemoveReplica present.

- **E2E: AddReplica(A) dispatch → complete.**
  runSettleLoop: dispatches, simulates revision bumps, completes. Final: 2 members, member type=Access, q unchanged, message = "Joined datamesh successfully".

---

## 2. RemoveReplica(A)

- **Dispatch: removes member.**
  2 members (D + A). Leave request for rv-1-1. Member removed from datamesh. Revision incremented. Transition has type=RemoveReplica, planID=access/v1.

- ⚡ **Guard: member attached → Detach fires first.**
  Access member with Attached=true. Leave request present. Outer loop iteration 1: attachment dispatcher creates Detach (Attached→false). Iteration 2: membership dispatcher re-dispatches RemoveReplica → guardNotAttached sees active Detach transition → blocks with "transition in progress" message. Detach still active (awaiting RVR confirmation).

- **Plan selection: not a member → skip.**
  Leave request for rv-1-1 but no member exists. planRemoveReplica returns skip. changed=false, no transition.

- **Settle: subject rev=0 → completed.**
  Active RemoveReplica(A). FM confirmed (rev 6). Subject reset revision to 0 (left datamesh). Transition completed. Message = "Left datamesh successfully".

- **Works when RV deleting.**
  RV has DeletionTimestamp. Leave request present. RemoveReplica NOT blocked by deletion — Leave transitions are always allowed.

- **E2E: RemoveReplica(A) dispatch → complete.**
  runSettleLoop: dispatches, simulates, completes. Final: 1 member, rv-1-1 removed, q unchanged, message = "Left datamesh successfully".

---

## 3. AddReplica(TB)

- **Dispatch: creates TieBreaker member.**
  1D existing. Join(TB) request for rv-1-1. Creates TieBreaker member with correct type and nodeName. Transition has planID=tiebreaker/v1. Revision incremented.

- ⚡ **VolumeAccess=Local does NOT block TB.**
  Configuration has VolumeAccess=Local. Join(TB) request. TB plan does not have guardVolumeAccessNotLocal — transition is created despite Local mode. This is correct: TB has no data and can serve as tiebreaker in Local mode.

- **Plan selection: already a member → skip.**
  rv-1-1 is already a TieBreaker member with a Join(TB) request. planAddReplica returns skip. changed=false.

- **Settle: completed.**
  Active AddReplica(TB). FM (D) + subject (TB) both confirmed. Transition completed. Message = "Joined datamesh successfully".

- **Settle: RemoveReplica(TB) subject rev=0 → completed.**
  Active RemoveReplica(TB). FM confirmed. Subject reset to rev=0. Completed. Message = "Left datamesh successfully".

---

## 4. RemoveReplica(TB)

- **Dispatch: removes TB member (odd D, TB not required).**
  3D+1TB layout (FTT=1, GMDR=1). Voters=3 (odd) → TB_min=0. Leave request for TB. Transition created, member removed.

- ⚡ **Guard: TB required — even D, FTT=D/2.**
  2D+1TB layout (FTT=1, GMDR=0). Voters=2 (even), FTT=1=D/2 → TB_min=1. TB_count=1 ≤ TB_min=1 → blocked. Message contains "TieBreaker required for quorum (Diskful=2 even, FTT=1)".

- **Guard: TB not required — odd D.**
  3D+1TB layout (FTT=1, GMDR=1). Voters=3 (odd) → TB_min=0. Guard passes. Transition created.

- **Plan selection: not a member → skip.**
  Leave request but no member. planRemoveReplica returns skip. changed=false.

---

## 5. AddReplica(sD)

- **Dispatch: creates 2-step transition, member = LiminalShadowDiskful.**
  1D existing. Join(sD) request with LVG/ThinPool. Creates member as LiminalShadowDiskful with BV fields (LVMVolumeGroupName, LVMVolumeGroupThinPoolName). Transition has planID=shadow-diskful/v1, 2 steps: "✦ → sD∅" (active), "sD∅ → sD" (pending).

- **Guard: ShadowDiskful feature not supported.**
  FeatureFlags.ShadowDiskful=false. Blocked with "ShadowDiskful not supported" message.

- **Step 1 confirmed → advances to step 2, type becomes sD.**
  Step 1 active at rev 6. All members confirmed. Step 1 completed. Step 2 applied: member type changed to ShadowDiskful. Step 2 now active.

- **Both steps confirmed → completed.**
  Step 1 completed. Step 2 active at rev 7. Subject confirmed (subjectOnly for step 2). Transition completed. Message = "Joined datamesh successfully".

- **Step 1 partial: not all confirmed.**
  D confirmed (rev 6), sD∅ not confirmed (rev 5). Transition stays. Progress "1/2 replicas confirmed revision 6".

---

## 6. RemoveReplica(sD)

- **Dispatch: single-step removal.**
  1D + 1sD. Leave request for sD. Single-step plan: "sD → ✕". Member removed immediately in apply. Transition has planID=shadow-diskful/v1, 1 step.

- **Settle: subject rev=0 → completed.**
  Active RemoveReplica(sD). D confirmed (rev 6). Subject reset to rev=0. Completed. Message = "Left datamesh successfully".

- ⚡ **Handles removal from liminal state (sD∅).**
  Member stuck in LiminalShadowDiskful (interrupted AddReplica). Leave request. Dispatch routes LiminalShadowDiskful to shadow-diskful/v1 plan. Removal proceeds normally.

- **Plan selection: not a member → skip.**
  Leave request but no member. skip. changed=false.

- **E2E: AddReplica(sD) 2-step dispatch → complete.**
  runSettleLoop with ShadowDiskful=true: dispatches, 2 steps settle. Final: member type=ShadowDiskful, LVG preserved, q=1.

- **E2E: RemoveReplica(sD) 1-step dispatch → complete.**
  runSettleLoop: dispatches, 1 step settles. Final: member removed, q=1.

---

## 7. AddReplica(D) — Dispatch

Plan selection depends on three axes: voter parity (even/odd), ShadowDiskful feature (on/off), and qmr raise needed (yes/no). 8 variants total.

- **Even voters, no sD, no qmr↑ → diskful/v1.**
  2D existing. Join(D) request. Voters=2 (even) → no q↑. sD off. qmr already correct. Plan = diskful/v1.

- **Even voters, no sD, qmr↑ needed → diskful-qmr-up/v1.**
  2D existing, config.GMDR=1, qmr=1 (needs 2). Plan = diskful-qmr-up/v1.

- **Odd voters, no sD, no qmr↑ → diskful-q-up/v1.**
  1D existing. Voters=1 (odd) → q↑ needed. Plan = diskful-q-up/v1.

- **Odd voters, no sD, qmr↑ needed → diskful-q-up-qmr-up/v1.**
  1D existing, config.GMDR=1. Plan = diskful-q-up-qmr-up/v1.

- **Even voters, sD, no qmr↑ → diskful-via-sd/v1.**
  2D existing, ShadowDiskful=true. Plan = diskful-via-sd/v1.

- **Even voters, sD, qmr↑ needed → diskful-via-sd-qmr-up/v1.**
  2D existing, ShadowDiskful=true, config.GMDR=1. Plan = diskful-via-sd-qmr-up/v1.

- **Odd voters, sD, no qmr↑ → diskful-via-sd-q-up/v1.**
  1D existing, ShadowDiskful=true. Plan = diskful-via-sd-q-up/v1.

- **Odd voters, sD, qmr↑ needed → diskful-via-sd-q-up-qmr-up/v1.**
  1D existing, ShadowDiskful=true, config.GMDR=1. Plan = diskful-via-sd-q-up-qmr-up/v1.

---

## 8. AddReplica(D) — Plan Behavior

### diskful/v1 (even→odd, 2 steps: ✦→D∅, D∅→D)

- **Creates transition, member = LiminalDiskful with BV.**
  2D existing. Step 1 applied: member created as LiminalDiskful with LVMVolumeGroupName and LVMVolumeGroupThinPoolName from request. 2 steps: "✦ → D∅" (active), "D∅ → D" (pending).

- **Step 1 confirmed → D∅→D: type becomes Diskful.**
  All 3 members confirmed rev 6. Step 1 completed. Step 2 applied: member type changed to Diskful. BV fields preserved.

- **Both steps confirmed → completed.**
  Step 2 active at rev 7. Subject confirmed (subjectOnly). Transition completed. Message = "Joined datamesh successfully".

- **Step 1 partial: not all confirmed.**
  2 of 3 confirmed. Transition stays. Progress "2/3".

### diskful-q-up/v1 (odd→even, 3 steps: ✦→A, A→D∅+q↑, D∅→D)

- ⚡ **Creates transition, member = Access (no BV).**
  1D existing. Step 1 applied: member created as Access (A vestibule). No LVG/ThinPool on member (A has no disk). 3 steps.

- ⚡ **Step 1 confirmed → A→D∅+q↑: type=LiminalDiskful, BV set, q raised.**
  FM (D) + subject (A) both at rev 6. Step 1 completed. Step 2 applied: type changed to LiminalDiskful, BV fields set from request, q raised (1→2 for 2 voters). Step 2 now active.

- **Step 2 confirmed → D∅→D.**
  All members confirmed. Step 2 completed. Step 3 applied: type=Diskful.

- **All steps confirmed → completed.**
  Step 3 active. Subject confirmed. Completed.

### diskful-qmr-up/v1 (even→odd, 3 steps: ✦→D∅, D∅→D, qmr↑)

- **Creates transition (3 steps).**
  2D existing, config.GMDR=1. Steps: "✦ → D∅", "D∅ → D", "qmr↑".

- ⚡ **D∅→D confirmed → qmr↑ applied.**
  Step 2 (D∅→D) confirmed by subject. Step 2 completed. Step 3 (qmr↑) applied: qmr raised (1→2).

- ⚡ **qmr↑ confirmed → completed, baseline updated.**
  All 3 members confirmed qmr↑ step. Transition completed. BaselineGuaranteedMinimumDataRedundancy updated to 1 (by step OnComplete via updateBaselineGMDR).

### diskful-q-up-qmr-up/v1 (odd→even, 4 steps)

- **Creates transition (4 steps): ✦→A, A→D∅+q↑, D∅→D, qmr↑.**
  1D existing, config.GMDR=1.

- ⚡ **Full lifecycle → q + qmr raised, baseline updated.**
  Last step (qmr↑) active at rev 9. All confirmed. Completed. q=2, qmr=2, baseline=1.

### diskful-via-sd/v1 (even→odd, sD, 3 steps: ✦→sD∅, sD∅→sD, sD→D)

- **Creates transition, member = LiminalShadowDiskful with BV.**
  2D existing, ShadowDiskful=true. Steps: "✦ → sD∅", "sD∅ → sD", "sD → D".

- **sD∅→sD confirmed → sD→D applied.**
  Step 2 (sD∅→sD) confirmed by subject. Step 2 completed. Step 3 applied: type=Diskful. confirmAllMembers for sD→D (all peers update arr/voting config).

- **All confirmed → completed.**
  All 3 members confirmed. Message = "Joined datamesh successfully".

### diskful-via-sd-q-up/v1 (odd→even, sD, 5 steps: ✦→sD∅, sD∅→sD, sD→sD∅, sD∅→D∅+q↑, D∅→D)

- **Creates transition (5 steps).**
  1D existing, ShadowDiskful=true. Steps include detach-before-promote (sD→sD∅→D∅+q↑) because q must be raised atomically with voter addition.

- ⚡ **sD→sD∅ confirmed → sD∅→D∅+q↑: BV preserved, q raised.**
  Step 3 (sD→sD∅) confirmed by subject. Step 3 completed. Step 4 applied: type=LiminalDiskful, BV fields preserved (already set), q raised (1→2).

- **Full lifecycle → completed.**
  Last step (D∅→D) confirmed. q=2.

### diskful-via-sd-qmr-up/v1 (even→odd, sD, 4 steps)

- **Creates transition (4 steps): ✦→sD∅, sD∅→sD, sD→D, qmr↑.**
  2D existing, ShadowDiskful=true, config.GMDR=1.

### diskful-via-sd-q-up-qmr-up/v1 (odd→even, sD, 6 steps)

- **Creates transition (6 steps): ✦→sD∅, sD∅→sD, sD→sD∅, sD∅→D∅+q↑, D∅→D, qmr↑.**
  1D existing, ShadowDiskful=true, config.GMDR=1.

- ⚡ **Full lifecycle → q + qmr raised, baseline updated.**
  Last step (qmr↑) confirmed. q=2, qmr=2, baseline=1.

### Additional

- ⚡ **q formula for larger layout: 3D + add D → q=3.**
  3D existing (odd). Join(D). Plan = diskful-q-up/v1. Step 1 (✦→A) applied: q NOT yet raised (A is non-voter, q stays 2). After step 1 confirmed → step 2 (A→D∅+q↑): 4 voters → q = 4/2+1 = 3.

---

## 9. RemoveReplica(D) — Dispatch

Plan selection depends on two axes: voter parity (odd/even) and qmr lower needed (yes/no). 4 variants.

- **Odd voters, no qmr↓ → remove-diskful/v1.**
  3D existing. Leave request. Voters=3 (odd) → removing makes even → no q↓ needed. qmr already correct. Plan = remove-diskful/v1.

- **Odd voters, qmr↓ needed → remove-diskful-qmr-down/v1.**
  3D existing, qmr=2 but config.GMDR=0 (target qmr=1). Plan = remove-diskful-qmr-down/v1.

- **Even voters, no qmr↓ → remove-diskful-q-down/v1.**
  2D existing. Voters=2 (even) → removing makes odd → q↓ needed. Plan = remove-diskful-q-down/v1.

- **Even voters, qmr↓ needed → remove-diskful-qmr-down-q-down/v1.**
  2D existing, qmr=2 but config.GMDR=0. Plan = remove-diskful-qmr-down-q-down/v1.

---

## 10. RemoveReplica(D) — Plan Behavior

### remove-diskful/v1 (odd→even, 2 steps: D→D∅, D∅→✕)

- **Creates transition, member becomes LiminalDiskful.**
  3D existing. Leave request. Step 1 applied: member type → LiminalDiskful. 2 steps: "D → D∅" (active), "D∅ → ✕" (pending).

- **Step 1 confirmed → D∅ removed from datamesh.**
  Step 1 (D→D∅) confirmed by subject (subjectOnly). Step 1 completed. Step 2 applied: member removed (removeMember). confirmAllMembersLeaving: must include the leaving subject explicitly (removeMember sets rctx.member=nil so allMemberIDs excludes it).

- **Both confirmed → completed.**
  Step 2 active. FM confirmed (rev 7). Subject reset to rev=0. Completed. Message = "Left datamesh successfully".

- **Not a member → skip.**
  Leave request but no member. changed=false.

### remove-diskful-q-down/v1 (even→odd, 3 steps: D→D∅, D∅→A+q↓, A→✕)

- **Creates transition (3 steps).**
  2D existing. Steps: "D → D∅", "D∅ → A + q↓", "A → ✕".

- ⚡ **D∅→A+q↓: type=Access, BV cleared, q lowered.**
  Step 1 (D→D∅) confirmed by subject. Step 2 applied: type changed to Access, LVMVolumeGroupName and LVMVolumeGroupThinPoolName cleared, q lowered (2→1). A is invisible to quorum (non-voter, star topology).

- **A removed → completed.**
  Step 3 active. FM confirmed. Subject reset to rev=0. Completed.

### remove-diskful-qmr-down/v1 (odd→even, 3 steps: qmr↓, D→D∅, D∅→✕)

- ⚡ **Creates transition (3 steps), qmr↓ FIRST.**
  3D existing, qmr=2, config.GMDR=0 (target qmr=1). Steps: "qmr↓" (first!), "D → D∅", "D∅ → ✕". qmr is lowered before D removal to relax quorum constraint.

- ⚡ **qmr↓ confirmed → advances to D→D∅.**
  All 3 members confirmed qmr↓ step. Step 1 completed. Step 2 applied: member type → LiminalDiskful. Baseline already updated in qmr↓ apply (lowering: compose with updateBaselineGMDR).

- **Completed.**
  All steps confirmed. Message = "Left datamesh successfully". BaselineGMDR = 0.

### remove-diskful-qmr-down-q-down/v1 (even→odd, 4 steps: qmr↓, D→D∅, D∅→A+q↓, A→✕)

- **Creates transition (4 steps).**
  2D existing, qmr=2, config.GMDR=0. Steps: "qmr↓", "D → D∅", "D∅ → A + q↓", "A → ✕".

- ⚡ **Full lifecycle → q + qmr lowered, baseline updated.**
  Last step completed. q=1, qmr=1, baseline=0.

### Guards

- ⚡ **guardNotAttached: member attached triggers Detach first.**
  3D, member #2 with Attached=true. Leave request. Outer loop: Detach dispatched first (Attached→false), then guardNotAttached sees active Detach → blocks RemoveReplica with "transition in progress" message.

- **guardFTTPreserved blocks removal.**
  2D with FTT=1: D_min = FTT + GMDR + 1 = 1+0+1 = 2. D_count=2 ≤ D_min=2 → blocked. Message contains "FTT".

- ⚡ **guardGMDRPreserved blocks when UpToDate D too low.**
  2D with config.GMDR=1. One D is NOT UpToDate. ADR = UpToDate_D - 1 = 1 - 1 = 0. 0 ≤ 1 → blocked. Message contains "GMDR".

- ⚡ **guardVolumeAccessLocal blocks attached D removal.**
  3D, member #1 Attached=true, VolumeAccess=Local. guardNotAttached fires first (attached → Detach dispatched). On second pass: active Detach → guardNotAttached blocks with "transition in progress".

- ⚡ **D∅ removal dispatch: LiminalDiskful member → remove plan selected.**
  Member stuck in LiminalDiskful (interrupted AddReplica). Leave request. Dispatch routes LiminalDiskful to remove-diskful plan (3 voters = odd → remove-diskful/v1).

---

## 11. ChangeReplicaType

### Non-voter pairs

- **A→TB: creates transition, member type becomes TieBreaker.**
  1D + 1A. ChangeRole(→TB) request for A member. Step applied: type changed to TieBreaker. Transition has planID=a-to-tb/v1. FromReplicaType=Access, ToReplicaType=TieBreaker. confirmFMPlusSubject.

- **A→TB: completed when FM + subject confirm.**
  All confirmed. Message = "Replica type changed successfully".

- **A→TB: already TB → skip.**
  Member already TieBreaker, ChangeRole(→TB) request. Skip. changed=false.

- **A→TB: not a member → skip.**
  ChangeRole request but no member. Skip.

- **TB→A: creates transition, member type becomes Access.**
  1D + 1TB (odd D, FTT=0 → TB not required). Step: TB→A. planID=tb-to-a/v1.

- **TB→A guard: VolumeAccess=Local blocks.**
  VolumeAccess=Local. Blocked with "volumeAccess is Local" message.

- ⚡ **TB→A guard: TB required (even D, FTT=D/2) blocks.**
  2D+1TB (FTT=1). Even D, FTT=D/2 → TB required. Blocked with "TieBreaker required" message.

- **TB→A guard: TB not required (odd D) passes.**
  3D+1TB (FTT=1). Odd D → TB not required. Transition created.

- **A→sD: creates 2-step transition (A→sD∅, sD∅→sD).**
  1D + 1A, ShadowDiskful=true. Step 1 applied: type=LiminalShadowDiskful, BV fields set. planID=a-to-sd/v1.

- **A→sD guard: ShadowDiskful feature not supported → blocked.**
  ShadowDiskful=false. Blocked with "ShadowDiskful not supported".

- **A→sD: step 1 confirmed → advances, type becomes sD.**
  Step 1 confirmed. Step 2 applied: type=ShadowDiskful. Step 2 active.

- **A→sD: both steps confirmed → completed.**
  Subject confirmed step 2 (subjectOnly). Message = "Replica type changed successfully".

- **sD→A: creates 2-step transition (sD→sD∅, sD∅→A).**
  Step 1 applied: type=LiminalShadowDiskful (disk detach). BV fields preserved. planID=sd-to-a/v1.

- **sD→A guard: VolumeAccess=Local blocks.**
  Blocked with "volumeAccess is Local".

- **sD→A: step 1 confirmed → step 2 applied, type=Access, BV cleared.**
  Subject confirmed step 1 (subjectOnly). Step 2 applied: type=Access, LVMVolumeGroupName and LVMVolumeGroupThinPoolName cleared. confirmAllMembers.

- **sD→A: both steps confirmed → completed.**

- ⚡ **sD→A: handles liminal state (sD∅→A).**
  Member stuck in LiminalShadowDiskful (interrupted A→sD). ChangeRole(→Access). Dispatch routes to sd-to-a/v1. Step 0 (sD→sD∅) is a no-op (already liminal) — apply returns changed=false, settle confirms immediately, engine advances to step 1 (sD∅→A) in the same call. Final type=Access.

- **TB→sD: creates 2-step transition (TB→sD∅, sD∅→sD).**
  1D + 1TB (odd D, TB not required). Step 1: type=LiminalShadowDiskful, BV fields set. planID=tb-to-sd/v1.

- **TB→sD guard: ShadowDiskful feature not supported → blocked.**
- ⚡ **TB→sD guard: TB required (even D, FTT=D/2) blocks.**
  2D+1TB (FTT=1). Blocked.
- **TB→sD: completes full lifecycle.**

- **sD→TB: creates 2-step transition (sD→sD∅, sD∅→TB).**
  Step 1: type=LiminalShadowDiskful (disk detach). BV preserved. planID=sd-to-tb/v1.

- **sD→TB guard: VolumeAccess=Local blocks.**
- **sD→TB: step 1 confirmed → step 2: type=TieBreaker, BV cleared.**
- **sD→TB: both confirmed → completed.**
- ⚡ **sD→TB: handles liminal state (sD∅→TB).**
  Already LiminalShadowDiskful. Step 0 is no-op → advances to step 1 (sD∅→TB) immediately. Final type=TieBreaker.

### Dispatch skip

- ⚡ **Skips when AddReplica in progress for same replica.**
  Active AddReplica(A) for rv-1-1 (not confirmed). ChangeRole request for rv-1-1. planChangeReplicaType returns skip (AddReplica must complete first). Only AddReplica transition present.

### Voter pairs: sD↔D

- **sD→D dispatch: even voters → sd-to-d/v1.**
  2D + 1sD. Voters=2 (even). Plan = sd-to-d/v1.

- **sD→D dispatch: odd voters → sd-to-d-q-up/v1.**
  1D + 1sD. Voters=1 (odd). Plan = sd-to-d-q-up/v1.

- **sd-to-d/v1: 1-step hot promotion, type=Diskful, BV preserved.**
  2D + 1sD. Step: sD→D (all peers update arr/voting). confirmAllMembers. BV preserved (both sD and D have backing volume).

- **sd-to-d/v1: completed → message.**

- ⚡ **sd-to-d-q-up/v1: 3-step detach-before-promote.**
  1D + sD. Steps: "sD → sD∅", "sD∅ → D∅ + q↑", "D∅ → D". sD∅→D∅+q↑: type=LiminalDiskful, BV preserved, q raised (1→2).

- **D→sD dispatch: odd voters → d-to-sd/v1.**
  3D. Voters=3 (odd). Plan = d-to-sd/v1.

- **D→sD dispatch: even voters → d-to-sd-q-down/v1.**
  2D. Voters=2 (even). Plan = d-to-sd-q-down/v1.

- **d-to-sd/v1: 1-step hot demotion, type=ShadowDiskful, BV preserved.**
  3D. Step: D→sD (all peers update). BV preserved.

- **d-to-sd/v1 guard: leavingDGuards block (1D, cannot demote last voter).**
  1D. ADR=0 ≤ 0 → GMDR guard blocks.

- ⚡ **d-to-sd-q-down/v1: 3-step (D→D∅, D∅→sD∅+q↓, sD∅→sD).**
  2D. Steps include D∅→sD∅+q↓: type=LiminalShadowDiskful, q lowered (2→1). BV preserved throughout.

### Voter pairs: A↔D

- **A→D dispatch: 4 variants (even/odd × sD/no-sD).**
  Even no sD → a-to-d/v1. Odd no sD → a-to-d-q-up/v1. Even sD → a-to-d-via-sd/v1. Odd sD → a-to-d-via-sd-q-up/v1.

- **a-to-d/v1: 2 steps (A→D∅, D∅→D), BV set.**
  2D + 1A. Step 1: type=LiminalDiskful, BV set. confirmAllMembers for A→D∅ (topology change). Step 2: type=Diskful. confirmSubjectOnly.

- ⚡ **a-to-d-q-up/v1: step 1 confirmed → q raised.**
  1D + 1A. Step 1 (A→D∅+q↑) confirmed. q raised. Type=LiminalDiskful, BV set.

- **a-to-d-via-sd/v1: 3 steps (A→sD∅, sD∅→sD, sD→D).**
  2D + 1A, ShadowDiskful=true.

- ⚡ **a-to-d-via-sd-q-up/v1: 5 steps (A→sD∅, sD∅→sD, sD→sD∅, sD∅→D∅+q↑, D∅→D).**
  1D + 1A, ShadowDiskful=true. Detach-before-promote sequence for q↑.

- **D→A dispatch: 2 variants (odd → d-to-a/v1, even → d-to-a-q-down/v1).**

- **d-to-a/v1: 2 steps (D→D∅, D∅→A), BV cleared.**
  3D. Step 2: type=Access, BV cleared. confirmAllMembers.

- ⚡ **d-to-a-q-down/v1: D∅→A+q↓ applied, q lowered, BV cleared.**
  2D. Step 2: type=Access, BV cleared, q lowered (2→1).

- **D→A guard: VolumeAccess=Local blocks.**
- **D→A guard: GMDR blocks.**
  2D with config.GMDR=1. ADR = UpToDate_D - 1. Not enough UpToDate → blocked.

### Voter pairs: TB↔D

- **TB→D dispatch: 4 variants (even/odd × sD/no-sD).**
  Even no sD → tb-to-d/v1. Odd no sD → tb-to-d-q-up/v1. Even sD → tb-to-d-via-sd/v1. Odd sD → tb-to-d-via-sd-q-up/v1.

- **tb-to-d/v1: 2 steps (TB→D∅, D∅→D), BV set.**
  2D + 1TB. Step 1: type=LiminalDiskful, BV set. confirmAllMembers.

- ⚡ **tb-to-d-q-up/v1: step confirmed → q raised.**
  1D + 1TB. Step 1 (TB→D∅+q↑). All confirmed → q raised.

- **tb-to-d-via-sd/v1: 3 steps (TB→sD∅, sD∅→sD, sD→D).**
- **tb-to-d-via-sd-q-up/v1: 5 steps.**

- ⚡ **TB→D guard: leavingTBGuards block when TB required.**
  2D+1TB (FTT=1). TB required. Blocked with "TieBreaker required" message.

- **D→TB dispatch: 2 variants (odd → d-to-tb/v1, even → d-to-tb-q-down/v1).**

- **d-to-tb/v1: 2 steps (D→D∅, D∅→TB), BV cleared.**
  3D. Step 2: type=TieBreaker, BV cleared. confirmAllMembers.

- ⚡ **d-to-tb-q-down/v1: D∅→TB+q↓, q lowered, BV cleared.**
  2D. Step 2: type=TieBreaker, BV cleared, q lowered (2→1).

- **D→TB guard: VolumeAccess=Local blocks.**
- **D→TB guard: GMDR blocks.**

### Liminal dispatch

- ⚡ **LiminalShadowDiskful → Diskful: routes to sd-to-d plan.**
  sD∅ member, ChangeRole(→Diskful). Even voters → sd-to-d/v1.

- ⚡ **LiminalDiskful → ShadowDiskful: routes to d-to-sd plan.**
  D∅ member, ChangeRole(→ShadowDiskful). Odd voters → d-to-sd/v1.

- ⚡ **LiminalDiskful → Access: routes to d-to-a plan.**
  D∅ member, ChangeRole(→Access). Odd voters → d-to-a/v1.

- ⚡ **LiminalDiskful → TieBreaker: routes to d-to-tb plan.**
  D∅ member, ChangeRole(→TieBreaker). Odd voters → d-to-tb/v1.

- ⚡ **LiminalDiskful → Diskful: skip (transition in progress).**
  D + D + D∅ member, ChangeRole(→Diskful). Dispatch returns skip (already transitioning). No transition created. changed=false.

- ⚡ **LiminalShadowDiskful → ShadowDiskful: skip (transition in progress).**
  D + D + sD∅ member, ChangeRole(→ShadowDiskful). Dispatch returns skip. No transition created. changed=false.

### ChangeType + automatic ChangeQuorum

- ⚡ **A→D + qmr raise: ChangeQuorum fires after ChangeType completes.**
  2D (q=2, qmr=2) + 1A. Config changed to GMDR=2 (target qmr=3). ChangeType(A→D) completes. Then ChangeQuorum dispatcher sees qmr diff → dispatches raise. Final: 3D, q=2, qmr=3, baseline=2.

- ⚡ **D→A + qmr lower: ChangeQuorum fires after ChangeType completes.**
  3D (q=2, qmr=2). Config changed to GMDR=0 (target qmr=1). ChangeType(D→A) completes. ChangeQuorum lowers qmr. Final: 2D+1A, q=2, qmr=1.

### E2E settle

- **a-to-tb/v1: 1-step settle.**
  runSettleLoop. Final: type=TieBreaker, q=1.

- **a-to-sd/v1: 2-step settle.**
  runSettleLoop with ShadowDiskful=true. Final: type=ShadowDiskful, BV preserved.

- **a-to-d/v1: 2-step settle.**
  runSettleLoop. Final: type=Diskful, q=2.

- **a-to-d-via-sd/v1: 3-step settle.**
  runSettleLoop with ShadowDiskful=true. Final: type=Diskful, q=2.

- ⚡ **a-to-d-via-sd-q-up/v1: 5-step settle.**
  runSettleLoop with ShadowDiskful=true. Final: type=Diskful, q=2 (raised mid-plan).

---

## 12. ForceRemoveReplica

### Dispatch per member type

- **ForceLeave(A) → access/v1.**
  1D + 1A. ForceLeave request for A. Plan = access/v1 (Emergency group).

- **ForceLeave(TB) → tiebreaker/v1.**

- **ForceLeave(sD) → shadow-diskful/v1.**

- ⚡ **ForceLeave(sD∅) → shadow-diskful/v1 (liminal).**
  Member in LiminalShadowDiskful state. Routes to shadow-diskful/v1.

- **ForceLeave(D) odd voters → diskful/v1.**
  3D. Voters=3 (odd) → no q↓.

- **ForceLeave(D) even voters → diskful-q-down/v1.**
  2D. Voters=2 (even) → q↓.

- ⚡ **ForceLeave(D∅) → diskful/v1 (liminal).**
  3D with one LiminalDiskful. Routes to diskful/v1 (3 voters = odd).

- ⚡ **Orphan member (no RVR, no request) → auto ForceRemove.**
  2 members: D(rv-1-0) has RVR, A(rv-1-1) has no RVR and no request. rv-1-1 is an orphan. Membership dispatcher auto-dispatches ForceRemoveReplica(access/v1). Member removed.

### Step flow

- **ForceRemove(A): member removed, transition active.**
  Member removed in apply (single step). Transition step = "Force remove".

- **ForceRemove(D) odd voters: member removed.**
  3D, ForceLeave for rv-1-2. Member removed. 2D remain. q unchanged (odd→even but ForceRemove does not adjust q in this variant — only even→odd variant has q↓).

- ⚡ **ForceRemove(D) + q↓: q lowered.**
  2D, ForceLeave for rv-1-1. Apply: removeMember + lowerQ (composed). Member removed. q lowered (2→1).

### Guards

- ⚡ **guardMemberUnreachable blocks: peer sees Connected.**
  1D + 1A. ForceLeave for A. D's RVR has DRBDConfigured=True and peer list shows A as Connected. Guard blocks with "reachable (connected from 1 replica(s))" message.

- ⚡ **guardMemberUnreachable passes: peer agent not ready (stale).**
  Same setup but D's RVR has DRBDConfigured reason=AgentNotReady. Guard skips this peer (stale data). No peers report Connected → guard passes. ForceRemove dispatched.

- ⚡ **guardNotAttached blocks: attached member with active RVA.**
  1D + A(attached). Active RVA keeps member settled (Attached + RVA = no Detach). ForceLeave blocked by guardNotAttached (member.Attached=true). Message contains "attached".

### Completion

- **Completed → message.**
  Active ForceRemove(access/v1). FM confirmed. Subject at rev=0. Completed. Message = "Force-removed from datamesh".

### CancelActiveOnCreate

- ⚡ **Existing AddReplica cancelled when ForceLeave dispatched.**
  In-flight AddReplica(A) for rv-1-1 (not yet confirmed). ForceLeave request replaces Join. CancelActiveOnCreate cancels AddReplica. ForceRemove created in its place.

### Emergency preemption

- ⚡ **Emergency preempts in-flight AddReplica(D).**
  3D layout. AddReplica(D)+q↑ dispatched for rv-1-3 (step 1: ✦→A applied). Replicas confirm. Then rv-1-2 dies: RVR removed, ForceLeave request added. ForceRemove runs (Emergency group — always allowed, preempts active Voter transition). System stabilizes: rv-1-2 removed, q correct for final voter count, no stuck transitions.

### Orphan scenarios

- ⚡ **Orphan D (even voters) → auto ForceRemove with q↓.**
  2D members, rv-1-1 has no RVR. Even voters → diskful-q-down/v1. Member removed, q lowered (2→1).

- ⚡ **Orphan attached D → full ForceDetach → ForceRemove cycle.**
  3D, rv-1-2 dies: RVR removed, member still Attached=true. Both ForceDetach and ForceLeave requests provided. ForceDetach runs first (attachment dispatcher), clears Attached (confirmImmediate). Then ForceRemove runs (membership dispatcher), removes member. Final: rv-1-2 removed, 2 surviving voters, q correct.

---

## 13. ChangeQuorum

### Dispatch

- **Skip: q and qmr already correct.**
  3D (q=2, qmr=2), config.GMDR=1. Both correct. No ChangeQuorum dispatched. changed=false.

- ⚡ **Skip: voting transition active.**
  Active AddReplica(D) (VotingMembership). qmr intentionally wrong (qmr=5). Quorum dispatcher skips entirely — voter count is changing, correct q is not yet determined. No ChangeQuorum created.

- ⚡ **Defer: qmr+1 with pending Join(D).**
  2D, config.GMDR=1, qmr=1 (needs 2). Join(D) request present. Quorum dispatcher defers to membership — embedded qmr↑ step in AddReplica(D) handles it. Only AddReplica dispatched, no ChangeQuorum.

- ⚡ **Defer: qmr-1 with pending Leave(D).**
  3D, qmr=2, config.GMDR=0 (needs 1). Leave request for D member. Deferred to membership — embedded qmr↓ step in RemoveReplica(D). Only RemoveReplica dispatched.

- **Dispatch: qmr differs, no matching D request.**
  2D, config.GMDR=1, qmr=1 (needs 2). No D request → ChangeQuorum dispatched.

- **Dispatch: qmr diff > 1.**
  3D, qmr=3 (needs 1). Diff=2 → cannot defer to membership (±1 only). ChangeQuorum dispatched.

- **Dispatch: q wrong.**
  3D, config.GMDR=1, qmr=2 (correct). But q=5 (wrong, should be 2). ChangeQuorum dispatched.

### Plan selection

- **Both lower → lower/v1.**
- **Both raise → raise/v1.**
- **Only qmr↑ → raise/v1.**
- **Only q↓ → lower/v1.**
- ⚡ **q↓ + qmr↑ → lower-q-raise-qmr/v1.**
- ⚡ **q↑ + qmr↓ → raise-q-lower-qmr/v1.**

### Step flow

- ⚡ **Corruption recovery: q=18, qmr=5 → fixed.**
  3D with q=18, qmr=5. runSettleLoop: ChangeQuorum dispatched and settles. Final: q=2, qmr=1. No active transitions.

- ⚡ **Corruption recovery: q=0, qmr=0 → fixed.**
  3D with q=0, qmr=0. Same result: q=2, qmr=1.

- **Diagonal up: 2D, qmr 1→2.**
  config.GMDR=1. ChangeQuorum raise. Final: q=2, qmr=2, baseline=1.

- **Diagonal down: 2D, qmr 2→1.**
  config.GMDR=0, qmr=2. ChangeQuorum lower. Final: q=2, qmr=1, baseline=0.

- ⚡ **lower/v1: qmr lowered, baseline updated in apply.**
  3D, qmr=3 (needs 1). Step 1 (qmr↓): qmr lowered, updateBaselineGMDR runs in apply (lowering). baseline=0.

- ⚡ **raise/v1: q+qmr raised, baseline updated in OnComplete.**
  2D, q=1 (wrong), qmr=1 (needs 2). Step 1 (q↑): q corrected. Step 2 (qmr↑): qmr raised, updateBaselineGMDR runs in OnComplete (raising, after all confirm). baseline=1.

### computeCorrectQuorum

- **1 voter, GMDR=0 → q=1, qmr=1.**
- **3 voters, GMDR=0 → q=2, qmr=1.**
- **4 voters, GMDR=0 → q=3, qmr=1.**
- **Lowering: qmr=3 → target 1.**
- **Full raise: 2 UpToDate ≥ target 2.**
- ⚡ **Partial raise: 2 UpToDate < target 3, raise to 2 (safe limit).**
  3 voters, config.GMDR=2 (target qmr=3). Only 2 UpToDate. safeQMR = min(3, 2) = 2. qmr raised to 2, not 3.
- ⚡ **Raise blocked: 0 UpToDate.**
  1 voter, config.GMDR=1 (target qmr=2). 0 UpToDate. safeQMR = min(2, 0) = 0. Cannot raise below current (1). qmr stays 1.
- **Already correct → no change.**

---

## 14. Attach

- **Dispatch: creates transition for member with active RVA.**
  1D member, active RVA on same node. Attach transition created. member.Attached set to true. Revision incremented. AttachmentConditionReason = "Attaching".

- **Guard: RV deleting → blocked.**
  RV has DeletionTimestamp. Blocked. AttachmentConditionMessage contains "is terminating". Reason = "ReplicatedVolumeTerminating".

- **Guard: quorum not satisfied → blocked.**
  1D member, RVR Ready but Quorum=false. guardQuorumSatisfied blocks. Message contains "Quorum not satisfied".

- **Guard: slot full → blocked.**
  2 members (D + A), MaxAttachments=1. D already attached (potentiallyAttached). Attach for A blocked with "slot (1/1 occupied)" message.

- ⚡ **Guard: VolumeAccess=Local blocks non-diskful member.**
  D on node-0 (voter for quorum), Access on node-1. VolumeAccess=Local. Attach for Access blocked. Reason = "VolumeAccessLocalityNotSatisfied".

- **Guard: VolumeAccess=Local allows Diskful member.**
  D member on node-1. VolumeAccess=Local. Attach proceeds — Diskful has backing volume locally.

- ⚡ **Guard: member not found — generic waiting.**
  D on node-0 (quorum), RVR on node-1 (Ready) but no member on node-1. Blocked with "Waiting for replica [#1] to join datamesh". Reason = "WaitingForReplica".

- ⚡ **Guard: member nil, RVR with Ready condition message.**
  D on node-0. RVR on node-1 with Ready=False, Reason=Initializing, Message="Creating backing volume". Blocked with message containing "Initializing" and "Creating backing volume".

- ⚡ **Guard: member nil, membership request in progress.**
  D on node-0. RVR on node-1 with pending Join request. Blocked with "Waiting for replica [#1] to join datamesh: <request message>".

- **Guard: node not eligible (with storage class name) → blocked.**
  RSP has node-2 but not node-1. Blocked with message naming the storage class and pool.

- ⚡ **Guard: node not eligible, no storage class → pool-only message.**
  Same but rv.Spec.ReplicatedStorageClassName is empty. Message mentions pool only, not "storage class".

- **Guard: node not ready → blocked.**
  RSP node with NodeReady=false. Message = "Node is not ready".

- **Guard: agent not ready → blocked.**
  RSP node with AgentReady=false. Message = "Agent is not ready on node".

- **Guard: RVR not Ready → blocked.**
  RVR exists, has Quorum=true but no Ready condition. Blocked. Reason = "WaitingForReplica".

- ⚡ **Guard: RVR not Ready with condition message.**
  RVR has Ready=False, Reason=Syncing, Message="Disk is syncing". Blocked message contains "Syncing" and "Disk is syncing".

- **Guard: RSP nil → blocked.**
  No RSP provided. Message contains "ReplicatedStoragePool".

- ⚡ **Guard: active membership transition → blocked.**
  Active AddReplica(A) for rv-1-1 (not confirmed). RVA on node-1. Attach blocked with "membership transition to complete". Reason = "WaitingForReplica".

- ⚡ **Guard: RVR nil (no RVR at all) → waiting for replica.**
  D member on node-1, active RVA. No RVR on node-1. Blocked with "Waiting for replica on node". Reason = "WaitingForReplica".

- ⚡ **Guard: VolumeAccess=Local, no storage class → pool-only message.**
  D on node-2 (quorum), A on node-1. VolumeAccess=Local, empty storage class name. Blocked message mentions "volumeAccess is Local" without "storage class".

- **Settled: already attached + active RVA → no-op.**
  D member Attached=true, active RVA, no active transition. ProcessTransitions returns changed=false. AttachmentConditionReason = "Attached". Message = "Volume is attached and ready to serve I/O on the node".

- ⚡ **Settled: already attached + RV deleting → pending deletion note.**
  D member Attached=true, active RVA, RV has DeletionTimestamp. Message contains "pending deletion". (Settled, not blocked — already attached stays attached.)

- ⚡ **FIFO: earlier RVA gets slot.**
  2 members (D + A), MaxAttachments=1. RVA on node-1 created at t=0, RVA on node-2 created at t=1. node-1 (older) gets the Attach slot. node-2 blocked by slot.

- ⚡ **FIFO: already-attached keeps slot over older RVA.**
  D on node-1 already attached. A on node-2 has OLDER RVA. Already-attached member settles (NoDispatch) and keeps slot via potentiallyAttached. node-2 blocked despite older RVA.

- ⚡ **maxAttachments=2: first Attach + EnableMultiattach, second waits.**
  2 members, MaxAttachments=2, multiattach=false. First node gets Attach. EnableMultiattach created. Second node blocked by tracker (multiattach toggle in progress). Message contains "multiattach".

- **Settle: Attach completed when confirmed.**
  Active Attach at rev 6. Subject confirmed. Transition completed.

- **Settle: Attach partial progress → message.**
  Active Attach at rev 6. Subject at rev 5. Message contains "0/1".

---

## 15. Detach

- **Dispatch: creates transition when attached + no active RVA.**
  D member Attached=true. No RVAs. Detach transition created. member.Attached set to false.

- **Guard: device in use → blocked.**
  D member Attached=true. rvr.Status.Attachment.InUse=true. Blocked. Message contains "device is in use". Reason = "Detaching".

- **Works when RV deleting.**
  RV has DeletionTimestamp. D member Attached=true. No RVAs. Detach proceeds — Leave/Detach are not blocked by deletion.

- ⚡ **Proceeds when RVR not Ready.**
  D member Attached=true, Quorum=true but no Ready condition on RVR. Detach plan only has guardDeviceNotInUse — no guardRVRReady. Detach proceeds.

- **Settle: Detach completed when confirmed.**
  Active Detach at rev 6. Subject confirmed. Completed.

- ⚡ **Settle: Detach when rvr gone (rvr=nil).**
  Active Detach at rev 6. Subject RVR completely gone (not in rvrs list). confirmSubjectOnlyLeavingOrGone accepts rvr=nil. Detach completed. (ForceRemove auto-dispatched for orphan member.)

- ⚡ **Settle: Detach when rvr revision=0.**
  Active Detach at rev 6. Subject RVR exists but revision=0 (left datamesh). confirmSubjectOnlyLeavingOrGone accepts rev=0. Detach completed.

- **Orphan: only deleting RVAs, no member → "detached" status.**
  No members. Deleting RVA on node-1. No transition. AttachmentConditionReason = "Detached". Message = "Volume has been detached from the node".

- **No transition for non-member node with deleting RVA.**
  No members. Deleting RVA on node-1. No Detach transition (nothing to detach).

- ⚡ **Reason stays Detaching while Detach transition is active.**
  D member Attached=true, deleting RVA. First ProcessTransitions creates Detach (member.Attached→false). Second ProcessTransitions without subject confirmation: transition still active. AttachmentConditionReason must be "Detaching" (from settle), not "Detached" (regression: dispatch NoDispatch used to overwrite settle status).

---

## 16. ForceDetach

- ⚡ **Dispatch + apply + settle in one call (confirmImmediate).**
  2D. rv-1-1 member Attached=true, ForceDetach request. confirmImmediate returns empty sets → step completes immediately. In one ProcessTransitions call: dispatched, applied (Attached→false), confirmed, completed. No ForceDetach transition remaining. member.Attached=false.

- ⚡ **Guard: MemberUnreachable blocks.**
  D member Attached=true. ForceDetach request. D's peer (another D) has DRBDConfigured=True and peer list shows target as Connected. guardMemberUnreachable blocks. Active RVA prevents normal Detach from firing. Member stays attached.

- ⚡ **CancelActiveOnCreate: in-flight Attach cancelled.**
  D member Attached=true. Active Attach transition for rv-1-1. ForceDetach request. CancelActiveOnCreate cancels the Attach. ForceDetach dispatched. Attached→false.

- ⚡ **ForceDetach → ForceLeave sequence.**
  D member Attached=false (already detached in previous cycle). ForceLeave request. ForceRemove dispatched. Member removed.

---

## 17. Multiattach

- **Enable: 2 intended attachments dispatches EnableMultiattach.**
  2 members (D + A), MaxAttachments=2. Active RVAs on both nodes. intendedCount=2 > 1 → EnableMultiattach dispatched. datamesh.multiattach set to true.

- **Disable: single intended attachment dispatches DisableMultiattach.**
  1 member, multiattach=true. Only 1 active RVA. intendedCount=1 → DisableMultiattach dispatched.

- ⚡ **Does not disable while potentiallyAttached > 1.**
  2 members both Attached=true, multiattach=true. Only 1 active RVA (node-1). Intended=1, wants disable. But node-2 still potentiallyAttached (Detach not confirmed yet). guardCanDisableMultiattach blocks: "2 nodes potentially attached".

- ⚡ **Does not create duplicate EnableMultiattach.**
  Active EnableMultiattach (not yet confirmed). 2 active RVAs. Dispatcher wants Enable again but tracker.CanAdmit blocks (hasMultiattachTransition=true). Still exactly 1 EnableMultiattach.

- **maxAttachments=1: dispatcher skips EnableMultiattach.**
  MaxAttachments=1 (default). 2 active RVAs. needMultiattach=false (maxAttachments=1) — dispatcher skips Enable. guardMaxAttachmentsAllowsMultiattach is defense-in-depth. multiattach stays false.

- **Guard: potentiallyAttached > 1 blocks DisableMultiattach.**
  2 members both Attached=true. No RVAs. Wants disable. But guardCanDisableMultiattach blocks.

- **Disables multiattach when maxAttachments decreased to 1 and potentiallyAttached <= 1.**
  1 member (D, Attached), maxAttachments=1, multiattach=true. 1 RVA. needMultiattach=false (maxAttachments=1). potentiallyAttached=1 ≤ 1 → DisableMultiattach dispatched.

- **Does not disable when maxAttachments=1 but potentiallyAttached > 1.**
  2 members both Attached, maxAttachments=1, multiattach=true. 2 RVAs. needMultiattach=false, but potentiallyAttached=2 > 1 → dispatcher skips Disable (must Detach first).

- **Settle: EnableMultiattach completes when all relevant members confirm.**
  Active EnableMultiattach at rev 6. D(Attached) + A(Attached) both confirmed. Completed.

- ⚡ **Settle: EnableMultiattach blocked while ShadowDiskful not confirmed.**
  Active EnableMultiattach at rev 6. D(Attached) confirmed. sD (not attached but has BV) at rev 5 — not confirmed. confirmMultiattach includes D+sD+Attached members → sD in mustConfirm. NOT completed.

- **Settle: DisableMultiattach completes.**
  Active DisableMultiattach at rev 6. Only 1 D member (has BV). Confirmed. Completed.

- ⚡ **Full cycle: Enable → attach both → detach one → Disable.**
  Phase 1: 2 members, MaxAttachments=2. Both have RVAs. EnableMultiattach + Attach(node-1) created. Phase 2: confirm both → EnableMultiattach settles, Attach(node-1) settles, Attach(node-2) created. Phase 3: confirm → both attached. Phase 4: remove RVA for node-2 → Detach(node-2). Phase 5: Detach settles → DisableMultiattach created. Phase 6: DisableMultiattach settles. Final: multiattach=false.

---

## 18. Attachment Combined

- ⚡ **Single-attach switch: detach old + attach new.**
  D on node-1 Attached=true. A on node-2. RVA only on node-2 (node-1 RVA removed). Detach created for node-1. node-2 wants Attach but slot occupied (potentiallyAttached includes node-1 until Detach confirms).

- **No-op when settled.**
  D Attached=true, active RVA, EffectiveLayout pre-settled. ProcessTransitions returns changed=false. No transitions.

- **Empty state: no members, no RVAs.**
  No members, no RVAs. ProcessTransitions returns changed=false.

- ⚡ **Settle + dispatch in same cycle.**
  Active Attach for node-1 (confirmed). RVA only on node-2. In one call: Attach(node-1) settles. Detach(node-1) dispatched (no active RVA). node-2 wants Attach but blocked by potentiallyAttached (node-1 Detach in progress).

- **Diagnostic error in progress message.**
  Active Attach at rev 6. Subject not confirmed. RVR has DRBDConfigured=False/ConfigurationFailed. Progress message contains "Errors" and "ConfigurationFailed".

- ⚡ **maxAttachments decrease: no forced detach.**
  2 members both Attached=true, MaxAttachments decreased to 1. Both have active RVAs. Engine does NOT force-detach — both settle as NoDispatch ("attached and ready"). Both stay attached. No Detach, no ForceDetach. changed=false.

- **E2E: attach/v1 dispatch → complete.**
  runSettleLoop. Final: member.Attached=true.

- **E2E: detach/v1 dispatch → complete.**
  runSettleLoop. Final: member.Attached=false.

- **E2E: force-detach/v1 dispatch → complete.**
  ForceDetach request. Settles in one iteration (confirmImmediate). member.Attached=false.

- **E2E: enable-multiattach/v1 dispatch → complete.**
  MaxAttachments=2, 2 RVAs. runSettleLoop. Final: multiattach=true.

- **E2E: disable-multiattach/v1 dispatch → complete.**
  1 member attached, multiattach=true, 1 RVA. runSettleLoop. Final: multiattach=false.

---

## 19. ResizeVolume

### Dispatch

- **Skip: datamesh.Size == spec.Size.**
  2D layout, spec.Size = datamesh.Size = 10Gi. ProcessTransitions returns changed=false. No transitions.

- **Skip: spec.Size < datamesh.Size (shrink not supported).**
  spec.Size = 5Gi, datamesh.Size = 10Gi. changed=false. No transitions.

- **Dispatch: datamesh.Size < spec.Size.**
  2D layout, spec.Size = 20Gi, datamesh.Size = 10Gi. All guards pass (Ready=True, Established, BV grown). ResizeVolume transition created with planID=resize/v1. datamesh.Size updated to spec.Size. Revision incremented.

### Guards

- **Guard: no ready diskful member.**
  D members have Ready=False. No ResizeVolume transition created.

- **Guard: active resync.**
  D member has peer in SyncTarget state. No ResizeVolume transition created.

- **Guard: backing volumes not grown.**
  D member BackingVolume.Size < target lower size. No ResizeVolume transition created.

### Settle

- **Partial confirm: some members not confirmed.**
  After dispatch, only 1 of 2 members bumps revision. Transition stays active.

- **All confirmed: transition completed.**
  All members bump revision. Transition completed. No transitions remain.

### E2E

- **Full lifecycle: dispatch → simulate → complete.**
  2D layout. runSettleLoop settles. Final: datamesh.Size == spec.Size, no transitions.

### Concurrency

- ⚡ **Resize blocked by active AddReplica(D).**
  Pre-existing AddReplica(D) transition + resize trigger. Tracker blocks ResizeVolume. Only AddReplica present.

- ⚡ **AddReplica(D) blocked by active resize.**
  Pre-existing ResizeVolume transition + Join(D) request. Tracker blocks AddReplica(D). Only ResizeVolume present.

---

## 20. Network: RepairNetworkAddresses

### Dispatch

- **Skip: addresses in sync.**
  1D member with address net-A:10.0.0.1:7000. RVR has same address. No transition. changed=false.

- **Dispatch: IP changed.**
  Member has net-A:10.0.0.1. RVR has net-A:10.0.0.99 (new IP). RepairNetworkAddresses dispatched. planID=repair/v1.

- **Dispatch: missing network in member.**
  Member has [net-A]. datamesh.systemNetworkNames = [net-A, net-B]. RVR has [net-A, net-B]. Member is missing net-B → dispatch.

- **Dispatch: stale network in member.**
  Member has [net-A, net-B]. datamesh.systemNetworkNames = [net-A]. Member has extra net-B → dispatch.

### Guard

- ⚡ **RVR addresses don't match datamesh target → blocks.**
  Member has wrong IP (dispatch triggers). But RVR has [net-A, net-B] while datamesh target is only [net-A]. guardReplicasMatchTargetNetworks blocks: RVR has extra net-B. No transition created.

### Settle

- **Apply syncs IP + confirm completes.**
  repairAddresses: member address updated to match RVR. After confirmation with peer connectivity, transition completes. Member address synced (IP=10.0.0.99, Port=9000).

- **Apply adds missing network.**
  Member had [net-A]. datamesh target [net-A, net-B]. repairAddresses adds net-B from RVR. Final: member has 2 addresses.

- **Apply removes stale network.**
  Member had [net-A, net-B]. datamesh target [net-A]. repairAddresses removes net-B. Final: member has 1 address.

---

## 21. Network: ChangeSystemNetworks

### Dispatch

- **Skip: RSP nil.**
  No RSP. No dispatch. changed=false.

- **Skip: networks in sync.**
  datamesh.systemNetworkNames = [net-A]. RSP.systemNetworkNames = [net-A]. No diff. No dispatch.

- **Dispatch: add/v1 — new network added.**
  datamesh = [net-A], RSP = [net-A, net-B]. added=[net-B], removed=∅. Plan = add/v1.

- **Dispatch: remove/v1 — network removed.**
  datamesh = [net-A, net-B], RSP = [net-A]. added=∅, removed=[net-B]. Plan = remove/v1.

- **Dispatch: update/v1 — add + remove with remaining.**
  datamesh = [net-A, net-B], RSP = [net-A, net-C]. remaining=[net-A]. Plan = update/v1.

- **Dispatch: migrate/v1 — full replacement, no remaining.**
  datamesh = [net-A], RSP = [net-B]. No intersection. Plan = migrate/v1.

- ⚡ **Priority: repair dispatched instead of CSN.**
  Member addresses out of sync AND networks differ. Repair takes priority. Only RepairNetworkAddresses dispatched, not ChangeSystemNetworks.

### Init

- ⚡ **Captures FromSystemNetworkNames and ToSystemNetworkNames.**
  CSN dispatched. Transition.FromSystemNetworkNames = current datamesh networks. Transition.ToSystemNetworkNames = RSP target. Frozen at dispatch time — even if RSP changes again mid-transition.

### Settle per plan

- **add/v1: [A] → [A, B].**
  Step 1 (Listen): adds net-B to datamesh.systemNetworkNames. Confirms when all RVRs report addresses on net-B. Step 2 (Connect): copies net-B addresses from RVR to members. Confirms when peer connections verified on net-B. Final: systemNetworkNames=[net-A, net-B], members have 2 addresses.

- **remove/v1: [A, B] → [A].**
  Guarded: remaining network (net-A) must have full connectivity. Step 1 (Disconnect): removes net-B from datamesh.systemNetworkNames + removes net-B addresses from members. Confirms when all members at revision. Final: systemNetworkNames=[net-A], members have 1 address.

- **update/v1: [A, B] → [A, C].**
  Guarded: remaining (net-A) connected. Step 1 (Listen new + Disconnect old): removes net-B from datamesh + addresses, adds net-C to datamesh. Confirms when RVRs report net-C addresses. Step 2 (Connect): copies net-C from RVR to members. Confirms on net-C connectivity. Final: systemNetworkNames=[net-A, net-C].

- **migrate/v1: [A] → [B].**
  No guard (no remaining to check). Step 1 (Listen new): adds net-B to datamesh. Step 2 (Connect new): copies net-B addresses. Step 3 (Disconnect old): removes net-A. Final: systemNetworkNames=[net-B].

### Multi-member

- **add/v1 with 2 members: both get new addresses.**
  1D + 1TB. Both members get net-B addresses from their respective RVRs with different IPs.

- **migrate/v1 with 2 members: full swap for both.**
  1D + 1TB. Both members swap from [net-A] to [net-B]. Each gets the correct IP for their node.

### Guard blocking

- ⚡ **remove/v1 blocked: no connectivity on remaining network.**
  2D. datamesh=[net-A, net-B], RSP=[net-A]. Peers connected only on net-B, NOT on net-A (remaining). guardRemainingNetworksConnected blocks.

- ⚡ **remove/v1 unblocked after connectivity established.**
  Same setup. Phase 1: blocked. Phase 2: establish connectivity on net-A. Phase 3: guard passes. remove/v1 completes.

### Edge cases

- **Multiple networks: [A] → [A, B, C].**
  add/v1 with 2 added networks. Both added to members. Final: 3 addresses per member.

- **Full swap: [A, B] → [C, D].**
  migrate/v1 with 2 removed + 2 added. Final: systemNetworkNames=[net-C, net-D], members have 2 addresses each on new networks.

---

## 22. Network Helpers

### peerConnected / peerConnectedOnNetwork

- **peerConnected: true for Connected peer.**
- **peerConnected: false for non-Connected (StandAlone) peer.**
- **peerConnected: false for missing peer.**
- **peerConnected: false for empty peer list.**
- **peerConnectedOnNetwork: true for correct network.**
- **peerConnectedOnNetwork: false for wrong network.**
- **peerConnectedOnNetwork: false for missing peer.**
- **peerConnectedOnNetwork: false for empty ConnectionEstablishedOn.**

### connectionVerified

- ⚡ **Exhaustive: all 7×7 side-state combinations.**
  Each side can be: nil, ready+missing, ready+Connected, ready+other, stale+missing, stale+Connected, stale+other. Verified iff at least one side is ready+Connected. Symmetry verified (a↔b same result).

- **b=nil → not verified.**
- ⚡ **minRevision: both sides rev=0, minRevision=10 → not verified.**
- **minRevision: one side rev=10 → verified (that side meets threshold).**
- **peerConnectedOnNetwork: correct network → verified; wrong network → not.**

### expectedPeerIDs

- **FM(D) sees all other members.**
- **Star(A) sees FM only.**
- **Star(TB) sees FM only (D + sD).**
- **FM(sD) sees all.**
- **FM(D∅) sees all.**
- **Self excluded (single member → empty).**
- **FM(sD∅) sees all.**

### allConnectionsOfMemberVerified / allMembersHaveFullConnectivity

- **2 FM members, both connected → true.**
- **2 FM members, not connected → false.**
- **With peerConnectedOnNetwork: correct net → true, wrong net → false.**
- **Single member (no peers) → true.**
- ⚡ **minRevision filtering: one side rev >= min → both verified (asymmetric confirmation).**
- ⚡ **minRevision not met by either side → false.**

### guardRemainingNetworksConnected

- **One remaining net fully connected → pass.**
- **No remaining net fully connected → blocked.**
- **Multiple remaining, one OK → pass.**
- **Empty intersection → blocked with "no common system networks" message.**
- **RSP unavailable → blocked with "waiting for ReplicatedStoragePool".**
- **Single member (no peers) → pass.**

### guardReplicasMatchTargetNetworks

- **All match → pass.**
- **Member missing network → blocked with "missing".**
- **Member has extra network → blocked.**
- **No target networks → pass.**
- **Member without RVR → skipped.**
- **Member with empty addresses → skipped.**

### addedNetworks / removedNetworks

- **nil transition → nil.**
- **from=[A], to=[A,B] → added=[B].**
- **from=[A,B], to=[A] → removed=[B].**
- **Full replacement: from=[A], to=[B] → added=[B], removed=[A].**

### confirmAddedAddressesAvailable

- **All confirmed: both RVRs have added address + rev.**
- **One missing address → only one confirmed.**
- **Rev not confirmed → empty.**
- **No transition → pure revision confirm.**
- **Multiple added: one RVR has both, other only one.**

### confirmAllConnectedOnAddedNetworks

- **All connected on added net → all confirmed.**
- **Not connected on added net → empty.**
- **Rev not confirmed → empty.**
- **No transition → pure revision confirm.**
- ⚡ **Partial: one side sees added net connected → both confirmed (asymmetric verification).**

### Apply callbacks

- **repairMemberAddresses: updates changed IP, no change when same, updates port, adds missing, removes stale, skips network not in RVR.**
- **repairAddresses: repairs address on shared network, no change when match, skips members without RVR, adds missing, removes stale.**
- **addNewAddresses: already present same IP → no change, different IP → updated, RVR has no address → skipped.**
- ⚡ **setSystemNetworks: panics when RSP is nil.**

---

## 23. Concurrency Tracker

### Basic admission

- **NonVoting allowed when no active transitions.**
- ⚡ **Per-member membership blocked for same replica.**
  Active AddReplica(NonVoting) for rv-1-3. New NonVoting proposal for rv-1-3 → blocked "membership transition in progress for this replica".
- **Attachment allowed alongside membership on same member.**
  Active AddReplica(NonVoting) for rv-1-3. Attachment proposal for rv-1-3 → allowed.
- ⚡ **Voter blocked by another Voter.**
  Active AddReplica(Voter) for rv-1-0. New Voter proposal for rv-1-5 → blocked "Another voting membership".
- **NonVoting alongside Voter on different member → allowed.**
- **NonVoting allowed when Quorum active.**
- ⚡ **Quorum blocked by Voter.**
  Active AddReplica(Voter). ChangeQuorum proposal → blocked "voting membership transition is active".
- **Quorum allowed when only NonVoting active.**
- ⚡ **Emergency always allowed (even when Voter active).**
- **Formation blocks everything (NonVoting, Voter, Attachment, Quorum, Multiattach).**
- ⚡ **Formation blocks Emergency.**
- **Duplicate Multiattach/Quorum/Formation blocked.**
- ⚡ **Per-member attachment blocked for same replica.**
  Active Attach for rv-1-3. New Attachment for rv-1-3 → blocked.
- **Remove unblocks subsequent CanAdmit.**
  Add Voter → blocked. Remove Voter → allowed.

### Quorum interactions

- ⚡ **Voter blocked by Quorum.**
  Active ChangeQuorum. VotingMembership proposal → blocked "ChangeQuorum".
- **Attachment allowed when Quorum active.**

### Multiattach and potentiallyAttached

- **Parallel Attachment with multiattach enabled → allowed.**
  multiattach=true. Active Attach for rv-1-0. New Attach for rv-1-5 → allowed.
- **Parallel NonVoting on different replicas → allowed.**
- ⚡ **First Attach allowed (empty potentiallyAttached).**
- ⚡ **Second Attach blocked without multiattach.**
  multiattach=false. First Attach adds to potentiallyAttached. Second Attach → blocked "multiattach".
- ⚡ **Attach blocked during EnableMultiattach toggle.**
  multiattach=true but EnableMultiattach active (hasMultiattachTransition=true). One member already attached. Second Attach → blocked "multiattach" (toggle not confirmed).
- ⚡ **Second Attach allowed with confirmed multiattach.**
  multiattach=true, no active Multiattach transition. One member attached. Second Attach → allowed.
- **Multiattach allowed when Attachment active.**
- ⚡ **Remove Detach clears potentiallyAttached.**
  One member attached (in potentiallyAttached). Second Attach blocked. Complete Detach for first member → Remove clears potentiallyAttached. Second Attach now → allowed.

### Details

- ⚡ **CanAdmit returns nil details for non-attachment proposals.**
  Formation blocks NonVoting. Details = nil.
- ⚡ **CanAdmit returns condition reason as details for attachment proposals.**
  Formation blocks Attachment. Details = "Pending" (used as RVA Attached condition reason).

### Network group

- ⚡ **Network blocks VotingMembership.**
  Active RepairNetworkAddresses (Network group). VotingMembership proposal → blocked "Network".
- ⚡ **Network blocks Quorum.**
  Active Network. Quorum proposal → blocked "Network".
- ⚡ **VotingMembership does NOT block Network.**
  Active AddReplica(Voter). Network proposal → allowed.
- ⚡ **Quorum does NOT block Network.**
  Active ChangeQuorum. Network proposal → allowed.
- **Network serialized (duplicate blocked).**
  Active Network. Another Network → blocked "network transition in progress".
- **Network does NOT block NonVoting or Attachment.**
- ⚡ **Formation does NOT block Network.**
  Active Formation. Network proposal → allowed (Network is semi-emergency).
- **Remove Network unblocks VotingMembership.**
  Active Network → Voter blocked. Remove Network → Voter allowed.

---

## 24. Effective Layout

### Change detection

- **First call on zero value → changed.**
- **Second call with same state → not changed.**
- **Count change detected (3D → 2D).**
- ⚡ **FTT nil vs non-nil transition detected.**
  Voters exist (FTT computed) → switch to no voters (FTT nil) → changed.

### FTT/GMDR unavailable

- **No replicas → nil.** Message = "No voters; FTT and GMDR unavailable: no voter members".
- **No members (only RVR, no member pointer) → nil.**
- **Only non-quorum types (Access, sD) → nil.** TotalVoters=0.
- ⚡ **Voters exist but all stale (AgentNotReady) → nil.** TotalVoters=2, StaleAgents=2. Message mentions "no fresh agent data" and stale IDs.
- **Voters exist but Quorum==nil for all → nil.** Same as stale.

### Direct classification (ready agents)

- **1D: Quorum=true + UpToDate → FTT=0, GMDR=0.**
  ReachableVoters=1, UpToDateVoters=1.
- ⚡ **1D: Quorum=true + NOT UpToDate → FTT=-1, GMDR=-1.**
  Reachable but no UpToDate data → negative values indicate degradation.
- ⚡ **1D: Quorum=false + UpToDate → FTT=-1, GMDR=0.**
  Has data but lost quorum.
- **3D: all OK → FTT=1, GMDR=1.**
  q=2, qmr=2. 3 reachable, 3 UpToDate.
- **3D: one Quorum=false → FTT=0.**
  2 reachable, 3 UpToDate. FTT = min(2-2, 3-2) = min(0, 1) = 0.
- **3D: one NOT UpToDate → FTT=0.**
  3 reachable, 2 UpToDate. FTT = min(3-2, 2-2) = min(1, 0) = 0.

### Peer reconstruction (stale agents)

- ⚡ **3D, one stale, peers see Connected+UpToDate → reachable+UpToDate.**
  2 ready D with Quorum=true. 1 stale D. Ready peers report stale as Connected+UpToDate. Reconstructed: 3 reachable, 3 UpToDate. FTT=1, GMDR=1.
- ⚡ **3D, one stale, peers see Connected but NOT UpToDate.**
  Reconstructed reachable (3) but UpToDate only 2. FTT=0.
- **3D, one stale, peers do NOT see it → not reconstructed.**
  Reachable=2, UpToDate=2. FTT=0.
- ⚡ **Stale agent's own peer data is IGNORED.**
  Stale agent reports peer as Connected. That peer data is not used (stale agent not in ready set). Only ready agents' peer observations count.
- ⚡ **2D+1TB, TB stale, peers see TB Connected → reachableTBs=1.**
  2D ready. 1TB stale. Ready D peer sees TB as Connected. ReachableTieBreakers=1. FTT=1 (even voters + reachable TB → tbBonus=1).
- **2D+1TB, TB stale, peers do NOT see TB → reachableTBs=0.**
  FTT=0 (no TB bonus).

### TB bonus

- **2D ready + 1TB ready: even voters + TB → tbBonus=1.**
  FTT = min(2-2+1, 2-1) = min(1, 1) = 1.
- **3D ready + 1TB ready: odd voters → tbBonus=0.**
  TB bonus only for even voters. FTT=1 (no bonus).
- **2D ready + 0TB: even but no TB → tbBonus=0.**
  FTT = min(2-2+0, 2-1) = min(0, 1) = 0.
- ⚡ **4D ready + 1TB stale, peer sees TB → reconstructed → tbBonus=1.**
  Even voters (4) + reconstructed TB. FTT=2.

### Formula edge cases

- ⚡ **FTT negative (quorum lost).**
  2D, both Quorum=false, both UpToDate. FTT = min(0-2, 2-1) = min(-2, 1) = -2.
- ⚡ **GMDR negative (no UpToDate).**
  1D, Quorum=true but no UpToDate. GMDR = min(0, 1) - 1 = -1.
- ⚡ **FTT limited by data, not quorum.**
  3D, all reachable (Quorum=true). Only 1 UpToDate. FTT = min(3-2, 1-2) = min(1, -1) = -1.
- ⚡ **GMDR capped by qmr.**
  3D, qmr=1. All UpToDate. GMDR = min(3, 1) - 1 = 0 (capped by qmr, not UpToDate count).

### Non-obvious interactions

- ⚡ **sD and LsD members are ignored (not voters, not TBs).**
  2D + 1sD. sD has Quorum=true and UpToDate. TotalVoters=2 (sD not counted).
- ⚡ **Member without RVR is skipped.**
  1D ready + 1D without RVR (member exists, rvr=nil). TotalVoters=1 (skip entry with nil rvr).
- **Peer reports for non-member IDs are harmless.**
  1D with peer "rv-1-5" Connected. ID 5 is not a member. TotalVoters=1, no crash.

---

## 25. Guards (Unit)

### guardMaxDiskMembers

- **7D members, adding 8th D → passes (7 < 8).**
- ⚡ **8D members, adding 9th D → blocked (8/8).**
  Message contains "maximum disk members reached (8/8".
- **7D + 1A, adding 8th D → passes (A doesn't count).**
- **7D + 1TB, adding 8th D → passes (TB doesn't count).**
- **8D, adding A → passes (non-disk not blocked).**
- ⚡ **7D + 1sD = 8 disk-bearing → blocked.**
- ⚡ **7D + 1D∅ (LiminalDiskful) = 8 → blocked.**
- ⚡ **7D + 1sD∅ (LiminalShadowDiskful) = 8 → blocked.**
- ⚡ **7D + A with ChangeType→D (pending) = 8 → blocked.**
  In-flight ChangeType(A→D) for rv-1-7. Pending transition counts toward disk limit.
- ⚡ **7D + A vestibule (AddReplica D, step at A) = 8 → blocked.**
  In-flight AddReplica(D) for rv-1-7 at step ✦→A (member currently A). Pending AddReplica with ReplicaType=Diskful counts.
- ⚡ **8D + A with ChangeType→D request → blocked.**
- ⚡ **8D + TB with ChangeType→sD request → blocked.**

### Zone guards

- **guardGMDRPreserved: 3D all UpToDate, GMDR=1 → ADR=2 > 1 → passes.**
- **guardGMDRPreserved: 2D all UpToDate, GMDR=1 → ADR=1 ≤ 1 → blocked.**
- **guardGMDRPreserved: 1D UpToDate, GMDR=0 → ADR=0 ≤ 0 → blocked.**
- **guardFTTPreserved: 3D, FTT=1 GMDR=1 → D=3 ≤ D_min=3 → blocked.**
- **guardFTTPreserved: 4D, FTT=1 GMDR=1 → D=4 > 3 → passes.**
- **guardFTTPreserved: 2D+TB, FTT=1 GMDR=0 → D=2 ≤ 2 → blocked.**
- **guardTBSufficient: 2D+1TB FTT=1 → last TB needed → blocked.**
- **guardTBSufficient: 2D+2TB FTT=1 → still 1 TB left → passes.**
- **guardTBSufficient: 3D+1TB FTT=1 → odd D, TB not required → passes.**
- **guardTBSufficient: 4D+1TB FTT=2 → even D, FTT=D/2 → blocked.**
- **guardTBSufficient: 4D+1TB FTT=1 → FTT≠D/2 → passes.**

### TransZonal guards

- **guardZoneGMDRPreserved: skip non-TransZonal.**
- ⚡ **guardZoneGMDRPreserved: 3D (1+1+1) GMDR=1, remove from zone-a → blocked.**
  After removal: losing zone-b → surviving=2-1=1 ≤ 1 → blocked.
- **guardZoneGMDRPreserved: 4D (2+1+1), remove from 2-zone → passes.**
- ⚡ **guardZoneGMDRPreserved: 5D (2+2+1) GMDR=2, remove → blocked.**
  Losing zone-b → surviving=4-2=2 ≤ 2 → blocked.
- ⚡ **guardZoneGMDRPreserved: 2D+TB GMDR=0, remove D → blocked.**
  After removal: losing remaining D's zone → 0 surviving ≤ 0 → blocked. (Non-zone guardGMDRPreserved would pass!)
- **guardZoneFTTPreserved: skip non-TransZonal.**
- ⚡ **guardZoneFTTPreserved: 3D (1+1+1), remove from zone-a → blocked.**
  After removal: 2D, losing zone-b → 1 < q=2, no TB → blocked.
- **guardZoneFTTPreserved: 2D+TB, remove D → passes (TB tiebreaker saves).**
- **guardZoneFTTPreserved: 4D+TB (2+1+1+TB), remove from 2-zone → passes.**
- ⚡ **guardZoneFTTPreserved: 5D (2+2+1), remove from 1-zone → blocked.**
  After removal: 4D, losing zone-a(2) → 2 < q=3 → blocked.
- **guardZoneTBSufficient: 2D+1TB TransZonal, remove last TB → blocked.**
- **guardZoneTBSufficient: 2D+2TB, remove one → passes.**
- **guardZoneTBSufficient: 3D+1TB, odd D → passes.**
- **guardZoneTBSufficient: 4D+1TB FTT=2 → blocked.**
- **guardZoneTBSufficient: 4D+1TB FTT=1 → FTT≠D/2 → passes.**

### Zonal guards

- **guardZonalSameZone: skip Ignored/TransZonal.**
- **guardZonalSameZone: same zone as primary → passes.**
- ⚡ **guardZonalSameZone: wrong zone (minority) → blocked.**
  2D in zone-a, 1D in zone-b. Primary = zone-a (most voters). Replica in zone-b → blocked.
- ⚡ **guardZonalSameZone: tie → both zones primary → passes.**
  1D in zone-a, 1D in zone-b. Tie. Either zone is acceptable.
- **guardZonalSameZone: D∅ counts as voter.**
- ⚡ **guardZonalSameZone: sD doesn't count as voter.**
  2sD in zone-a, 1D in zone-b. Primary = zone-b (only voter). Replica in zone-a → blocked.
- **guardZonalSameZone: zone from member (ChangeType) or RSP (AddReplica).**
- **guardZonalSameZone: no member and no RSP → skip (other guards block).**

### TransZonal placement guards

- **guardTransZonalVoterPlacement: skip non-TransZonal.**
- **guardTransZonalVoterPlacement: 3D (1+1+1), add to 1D zone → passes.**
- **guardTransZonalVoterPlacement: 2D+TB, add to 0D zone → passes.**
- ⚡ **guardTransZonalVoterPlacement: 5D (2+2+1), add to 2-zone → blocked.**
  3+2+1 after add. Losing zone-a(3) → 3 < q=4 → blocked.
- **guardTransZonalVoterPlacement: 4D+TB, add to 1-zone → passes.**
- **guardTransZonalVoterPlacement: add to new zone (zone-d) → passes.**
- **guardTransZonalTBPlacement: skip non-TransZonal.**
- **guardTransZonalTBPlacement: TB in zone with 0D → passes.**
- **guardTransZonalTBPlacement: TB in zone with 1D → passes.**
- ⚡ **guardTransZonalTBPlacement: TB in zone with 2D → blocked.**
  "zone already has 2 Diskful voters (TB zone must have at most 1)".

### Other guards

- ⚡ **guardMemberUnreachable: no peers → passes.**
- ⚡ **guardMemberUnreachable: peer sees Connected → blocked.**
  "reachable (connected from 1 replica(s))".
- ⚡ **guardMemberUnreachable: peer Connected but agent not ready (stale) → passes.**
- **guardMemberUnreachable: peer non-Connected → passes.**
- ⚡ **guardMemberUnreachable: multiple peers, two Connected → "2 replica(s)".**
- **guardNotAttached: not attached, no transition → passes.**
- **guardNotAttached: attached → blocked.**
- **guardNotAttached: member nil → passes.**
- ⚡ **guardNotAttached: not attached but Attach transition in progress → blocked.**
- ⚡ **guardNotAttached: not attached but Detach transition in progress → blocked.**
- **guardVolumeAccessLocalForDemotion: not attached → passes.**
- **guardVolumeAccessLocalForDemotion: attached + Local → blocked.**
- **guardVolumeAccessLocalForDemotion: attached + non-Local → passes.**
- **guardVotersEven: 2 voters → passes; 3 → blocked.**
- **guardVotersOdd: 3 voters → passes; 2 → blocked.**
- **guardQMRRaiseNeeded: qmr < target → passes; qmr ≥ target → blocked.**
- **guardQMRLowerNeeded: qmr > target → passes; qmr ≤ target → blocked.**
- **guardShadowDiskfulSupported: feature on → passes; off → blocked.**

---

## 26. Integration: Quorum Invariants

All 7 canonical layouts are tested across topologies and feature variants.
Each layout runs a 4-step cycle checking q and qmr invariants at every intermediate state.

**Layouts:** 1D (0,0), 2D+TB (1,0), 2D (0,1), 3D (1,1), 4D+TB (2,1), 4D (1,2), 5D (2,2)

**Topologies:** Ignored, TransZonal 3z (excludes 4D), TransZonal 4z (4D only), TransZonal 5z (4D+TB and 5D), Zonal

**Feature variants:** default (A-vestibule), ShadowDiskful (sD-vestibule)

**Invariants checked at every step (assertSafetyInvariants):**
- q == voters/2 + 1 (majority quorum, no split-brain)
- qmr >= 1 (minimum valid redundancy)

**4-step cycle per layout:**

- ⚡ **Step 1: Remove D at minimum → blocked.** Guards (FTT, GMDR) prevent removing when at the minimum viable D count.
- **Step 2: Add +1, +2, +3 D sequentially.** q invariant verified after each add.
- **Step 3: Remove -1, -2, -3 D back to minimum.** q invariant verified after each remove.
- ⚡ **Step 4: Remove D at minimum again → still blocked.** Confirms guards work identically after a full add/remove cycle.

---

## 27. Integration: ForceRemove Cascade

Tests cascading ForceRemove operations simulating sequential node failures.
All layouts × topologies × feature variants.

**Per-layout cycle:**

- ⚡ **Sequential ForceRemove D members down to 1D.**
  Remove last D first, then second-to-last, etc. After each:
  - qmr must NEVER decrease (data redundancy floor preserved).
  - BaselineGMDR must NEVER decrease (published API value stable).
  - q must equal voters/2+1.

- **ForceRemove TB (if present): qmr and baseline still preserved.**

- ⚡ **Recovery: lower config.GMDR to 0 after catastrophic loss.**
  1D remaining with high qmr (from original layout). User sets config.GMDR=0, config.FTT=0. ChangeQuorum fires: qmr→1, baseline→0. IO restored.

**Simultaneous ForceRemove (multiple dead nodes in one cycle):**

- ⚡ **2 D die simultaneously from 3D.**
  ForceLeave for rv-1-1 and rv-1-2. Both removed. 1D remaining. q=1. qmr preserved.

- ⚡ **D + TB die simultaneously from 4D+TB.**
  ForceLeave for rv-1-3 (D) and rv-1-4 (TB). Both removed. 3D remaining, 0TB. q=2. qmr preserved.

- ⚡ **2 D die simultaneously from 5D.**
  ForceLeave for rv-1-3 and rv-1-4. Both removed. 3D remaining. q=2. qmr preserved.

---

## 28. Integration: Attachment Lifecycle

- **Full D lifecycle: add+attach → detach+remove.**
  2D existing. Phase 1: Join(D) + RVA → AddReplica(D) completes, then Attach completes (member Attached=true). Phase 2: remove RVA + Leave request → Detach completes, then RemoveReplica(D) completes (member removed). Final: 2D, q correct.

- **Full A lifecycle: add+attach → detach+remove.**
  1D existing. Phase 1: Join(A) + RVA → AddReplica(A) + Attach. Phase 2: remove RVA + Leave → Detach + RemoveReplica(A).

- ⚡ **Concurrent: Attach on node-1 + AddReplica(D) on node-3.**
  2D + Join(D) for node-3 + RVA on node-1. Both complete concurrently (Attachment and VotingMembership can run in parallel on different members). Final: 3D+, node-1 attached.

- ⚡ **Remove blocked by attachment: detach-before-remove sequencing.**
  2D, rv-1-1 Attached=true. Leave request for rv-1-1. No RVA. guardNotAttached blocks RemoveReplica. Detach fires first. After Detach settles → RemoveReplica proceeds. Final: 1D.

- ⚡ **Multiattach lifecycle: enable → attach 2 → detach 1 → disable.**
  2D, MaxAttachments=2. Phase 1: EnableMultiattach + Attach both. Phase 2: remove RVA for node-2 → Detach + DisableMultiattach. Final: multiattach=false, node-1 attached, node-2 detached.

- ⚡ **VolumeAccess=Local: only Diskful members attach.**
  3 members (D+D+A), MaxAttachments=3, VolumeAccess=Local. RVAs on all 3 nodes. D members attach. A member blocked by guardVolumeAccessLocalForAttach. Final: D members Attached=true, A Attached=false.

- ⚡ **Attached member dies: ForceDetach + ForceRemove.**
  2D, rv-1-1 Attached=true. Node dies: RVR removed. ForceLeave request. ForceDetach clears Attached (confirmImmediate). ForceRemove removes member. Final: 1D.

- ⚡ **Quorum lost → attach blocked → restored → attach succeeds.**
  2D, both Quorum=false. RVA on node-1. Phase 1: guardQuorumSatisfied blocks Attach. Message = "Quorum not satisfied". Phase 2: restore Quorum=true on both. Attach proceeds. Final: node-1 attached.

---

## 29. Integration: Layout Transitions

All 7 canonical layouts form ordered pairs (42 transitions). Each pair is tested in two modes across topologies and feature variants.

**Layouts:** 1D, 2D+TB, 2D, 3D, 4D+TB, 4D, 5D

**Topologies:** Ignored, Zonal, TransZonal 3z (excludes 4D), TransZonal 4z (4D only), TransZonal 5z (4D+TB and 5D)

**Feature variants:** default, ShadowDiskful

**Strategies:**

- ⚡ **All at once:** All requests (Join D, Leave D, Join TB, Leave TB) provided simultaneously. Engine processes them in the correct order via dispatch + guards + concurrency tracker. Verify final D/TB counts, q = voters/2+1, qmr = config.GMDR+1.

- ⚡ **Step by step (D-first):** Requests fed one at a time, D operations before TB operations. q invariant checked after each step.

- ⚡ **Step by step (TB-first):** For transitions involving both D and TB changes, TB operations fed first. Guards enforce safe ordering (e.g. TB removal blocked until D count makes TB optional).

**TransZonal relaxation:** Zone guards may block some additions/removals. System must be stable (no stuck transitions) and q correct for the actual voter count, even if the target layout is not fully achieved.

---

## 30. Integration: Topology Switch

- ⚡ **Ignored → TransZonal: stable layout, no spurious transitions.**
  3D Ignored with zones already set on members. Switch to TransZonal. No membership transitions should be created by the topology switch alone.

- ⚡ **TransZonal → Ignored: blocked Leave becomes possible.**
  4D TransZonal 3z (2+1+1). Leave request for rv-1-2 (zone-b, 1D zone). Zone FTT blocks in TransZonal (losing zone-a(2D) after removal → insufficient). Switch to Ignored: zone guards off. Leave proceeds. Final: 3D.

- ⚡ **Ignored → Zonal: Add D to wrong zone blocked.**
  2D Zonal in zone-a. Join(D) for node in zone-b. guardZonalSameZone blocks: zone-b ≠ primary zone-a. Message contains "primary zone".

- ⚡ **Zonal → Ignored: blocked Add becomes possible.**
  Phase 1: Zonal blocks Add to zone-b. Phase 2: switch to Ignored. Add proceeds. Final: 3D.

- ⚡ **Ignored → TransZonal: stale zones block placement.**
  3D Ignored with Zone="" on all members (no zone info). Switch to TransZonal. RSP has zones. guardTransZonalVoterPlacement blocks Add because existing members have Zone="" (all 3 voters in empty zone — losing that zone is catastrophic). Add blocked.

- ⚡ **ForceRemove after topology switch bypasses zone guards.**
  3D Ignored with zones. Switch to TransZonal. rv-1-2 dies (RVR removed). ForceLeave request. ForceRemove completes — Emergency group bypasses all guards including zone guards. Final: 2D.

- ⚡ **Zonal → TransZonal: guards switch correctly.**
  2D Zonal (1D zone-a, 1D zone-b). Add request for node in zone-c. Phase 1: Zonal — zone-c not a primary zone → blocked. Phase 2: switch to TransZonal. zone-c is a new zone, 1+1+1 balanced → passes. Final: 3D.

- ⚡ **4D blocked in 3-zone TransZonal.**
  3D TransZonal 3z (1+1+1). Config changed to FTT=1, GMDR=2 (target: 4D, q=3, qmr=3). Join(D) request. guardTransZonalVoterPlacement blocks: 2+1+1 distribution, losing 2D zone → 2D < qmr=3. 4D is not achievable in 3 zones with qmr=3.

- ⚡ **D↔TB swap in 2D+TB (TZ 3z).**
  D(zone-a) + D(zone-b) + TB(zone-c). Goal: swap D(zone-a) with TB(zone-c). 4 operations: AddD(zone-c), AddTB(zone-a), Leave(TB@zone-c), Leave(D@zone-a). Final: 2D+1TB, q=2, all zones covered.

- ⚡ **D↔TB swap in 4D+TB (TZ 3z).**
  D+D(zone-a) + D(zone-b) + D+TB(zone-c). Goal: swap D from zone-a with TB from zone-c. 4 operations. Final: 4D+1TB, q=3, system stable.

---

## 31. Integration: Network

- **Sequential CSN: add then remove.**
  2D. Phase 1: RSP adds net-B → add/v1 completes (systemNetworkNames=[A,B], members have 2 addresses). Phase 2: RSP removes net-B → remove/v1 completes (systemNetworkNames=[A], members have 1 address).

- **Sequential CSN: add then migrate.**
  2D. Phase 1: add net-B. Phase 2: RSP changes to [net-C] only → migrate/v1 completes (full replacement). Final: systemNetworkNames=[C].

- ⚡ **CSN + concurrent AddReplica(D).**
  2D. Join(D) request + RSP adds net-B. Both complete concurrently (Network does not block NonVotingMembership; VotingMembership blocked by Network but Network completes first, then Voter runs). Final: systemNetworkNames=[A,B], 3D+.

- ⚡ **CSN + concurrent RemoveReplica(D).**
  3D. Leave request + RSP removes net-B (with connectivity on remaining net-A). Both complete. Final: systemNetworkNames=[A], 2D.

- ⚡ **Repair + concurrent AddReplica(A).**
  1D with stale address (member IP ≠ RVR IP). Join(A) request. Repair fires (network dispatcher priority). NonVoting join also dispatched (Network does not block NonVoting). Both complete. Final: address synced, A member added.

- **Multi-member network add (3D+TB).**
  4 members. RSP adds net-B. add/v1 completes. All 4 members get 2 addresses each with correct per-node IPs.

---

## 32. Context and Writeback

### buildContexts

- **Collects IDs from Members.**
  RV has members rv-1-0 (D) and rv-1-3 (A). Contexts created for IDs 0 and 3. ID 5 → nil.

- **Collects IDs from RVRs.**
  RVR rv-1-2. Context created for ID 2 with rvr pointer, nodeName, name.

- **Collects IDs from Requests.**
  DatameshReplicaRequest rv-1-7. Context created for ID 7 with membershipRequest pointer.

- **Collects IDs from replica-scoped Transitions.**
  Transition with ReplicaName=rv-1-4. Context created for ID 4.

- ⚡ **Skips global transitions when collecting IDs.**
  Global transition (no ReplicaName, type=EnableMultiattach). No replica contexts created.

- **Populates RVR + Member + Request on same replica.**
  All three sources refer to rv-1-5. Single context has rvr, member, and membershipRequest all set.

- ⚡ **NodeName priority: RVR > Member.**
  RVR has NodeName=rvr-node. Member has NodeName=member-node. Context.nodeName = rvr-node.

- **Name priority: RVR > Member > Request.**
  Member and Request both named rv-1-2 (no RVR). Name from Member. When RVR exists, RVR.Name takes priority.

- **Assigns RVAs by NodeName.**
  Member rv-1-0 on node-a. RVA on node-a. Context for ID 0 gets rvas=[1 entry].

- ⚡ **Appends orphan RVA nodes.**
  Member on node-a. RVA on node-orphan (no member/RVR). Orphan appended to allReplicas (len=2) but NOT in replicas[32] index.

- ⚡ **Transitions NOT populated on ReplicaContexts.**
  Active AddReplica(NonVoting) for rv-1-0 in transitions list. After buildContexts: rctx.membershipTransition = nil. Engine populates via slot accessors in NewEngine.

- **GlobalContext populated.**
  ShadowDiskful=true → gctx.features.ShadowDiskful = true.

- **Handles empty state.**
  No members, no RVRs, no requests, no transitions. allReplicas empty. All replicas[0..31] = nil.

- ⚡ **Member pointers point into rv.Status.Datamesh.Members.**
  Pointers are BeIdenticalTo (same address), not just equal. Stable during engine processing.

- ⚡ **Backing array reallocation detection for orphan appends.**
  2 known IDs + 2 orphan RVA nodes → forces reallocation. After reallocation, replicas[32] index rebuilt. Known replicas still accessible by ID.

- **Request pointers point into rv.Status.DatameshReplicaRequests.**

- **gctx set on all ReplicaContexts including orphans.**

### writebackMembersFromContexts

- **Fast path: no changes when set and pointers match.**
  Same member set, same pointer addresses. No-op.

- **Member added: new heap-allocated member appended.**
  rctx.member set to new object. Writeback appends it. Order: surviving first, new at end.

- **Member removed: member set to nil.**
  rctx.member = nil. Writeback compacts. Members count decreases.

- **Member replaced: pointer changed to new heap object.**
  rctx.member pointed to a new object (e.g. type change). Old entry replaced.

- **Mixed: one surviving, one removed, one added.**
  3 members initially. Keep 0, remove 1, add 5. Final: 2 members, order = [rv-1-0, rv-1-5].

### writebackDatameshFromContext / writebackBaselineGMDRFromContext

- **Writes scalar parameters back to rv.Status.**
  gctx.datamesh.quorum=3, qmr=2, multiattach=true → rv.Status.Datamesh reflects same.

- **Writes baselineGMDR (including zero).**
  gctx.baselineGMDR=2 → rv.Status.BaselineGuaranteedMinimumDataRedundancy=2. Also works for 0.

### writebackRequestMessagesFromContexts

- **Returns false when no requests exist.**
- **Returns false when message unchanged.**
  Request message = "existing". rctx.membershipMessage = "existing". No change.
- **Returns true and updates when message changed.**
  In-place update: request.Message updated to match rctx.membershipMessage.
- **Mixed: some changed, some unchanged.**
  2 requests. First unchanged, second updated. Returns true.

### isQuorumSatisfied

- **Satisfied when voter has Quorum=true.**
- **Not satisfied: voter has Quorum=false.** Diagnostic = "no quorum on [#0]".
- **Not satisfied: no voter members.** Diagnostic = "no voter members".
- ⚡ **Skips agent-not-ready voters.** Voter has Ready condition with reason=AgentNotReady. Skipped (stale). Diagnostic mentions "agent not ready".
- ⚡ **Mixed: one voter no quorum + one agent-not-ready.** Diagnostic mentions both.
- ⚡ **Caches result.** First call computes. Second call returns cached value even if allReplicas mutated.
- ⚡ **Voter without RVR is skipped.** Member exists but rvr=nil. Treated as "no voter members" (needs rvr to check quorum).

### getEligibleNode

- **Returns eligible node when found.**
- **Returns nil when node not in RSP.**
- **Returns nil when RSP is nil.**
- ⚡ **Caches result.** First and second call return same pointer. eligibleNodeChecked=true.

### updateMemberZonesFromRSP

- **No RSP → no changes (returns false).**
- **No members → no changes.**
- **Zone already correct → no changes.**
- **Zone updated from RSP.**
  Member.Zone="" → updated to "a" from RSP. Returns true.
- **Node not in RSP → skip.** Member on node-unknown. RSP has no entry. Zone unchanged.
- **Multiple members, one changes.**
  2 members. node-0 already correct. node-1 zone empty → updated. Returns true.

---

## 33. Performance

### Allocation bounds (Ginkgo tests)

- **No-op 1D: < 30 allocs per ProcessTransitions call.**
- **No-op 3D: < 30 allocs.**
- **No-op 5D: < 30 allocs.**
- **No-op 4D+1TB: < 30 allocs.**
- **No-op 8D: < 30 allocs.**
- **No-op 16D: < 30 allocs.**
- **No-op 32D: < 30 allocs.**

### Feature and topology variants

- ⚡ **sD variant same order as default.**
  ShadowDiskful=true: allocs within 1.5x + 10 of default.
- **No-op 5D Zonal: < 30 allocs.**
- **No-op 5D TransZonal 3z: < 30 allocs.**
- **No-op 32D TransZonal 5z: < 30 allocs.**
- ⚡ **Topology overhead: 5D TZ vs Ignored within 2x.**

### Scaling

- ⚡ **32D no-op ≤ 5x of 5D no-op.**
  Alloc ratio must be under 5.0 — engine overhead grows sub-linearly with member count.

### Go benchmarks (BenchmarkXxx, not Ginkgo)

- **BenchmarkNoOp: 1D, 3D, 5D, 4D+1TB, 8D, 16D, 32D, 5D sD, 5D TZ, 5D Zonal, 32D TZ.**
- **BenchmarkDispatch_AddD_3D: single dispatch with full guard chain.**
- **BenchmarkDispatch_AddD_3D_TransZonal: dispatch with TransZonal zone guards (heaviest chain).**
- **BenchmarkSettle_Confirm_3D: settling active transition with partial confirmation.**
- **BenchmarkFullCycle_AddD_3D: complete lifecycle via runSettleLoop.**
- **BenchmarkFullCycle_ForceRemove_3D: complete ForceRemove lifecycle.**
