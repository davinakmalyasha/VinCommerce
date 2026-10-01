import io

# --- 1. the two false passes in my own tests: assert the CONDITION, not the error string ---
p = 'internal/service/payout_lag_override_test.go'
s = io.open(p, encoding='utf-8').read()

old = '''	// Every settled state is acceptable, not only `approved`: a batch that has
	// already been submitted or paid must still be exportable for the bank's
	// acknowledgement, and refusing would leave an operator unable to prove a
	// transfer happened.
	for _, ok := range []string{"PayoutBatchApproved", "PayoutBatchSubmitted", "PayoutBatchPaid"} {'''
new = '''	// The GUARD, not the error code. Asserting that "PAYOUT_BATCH_NOT_APPROVED"
	// appears in the body passes just as well on `if false &&` -- the message is
	// still sitting there in dead code. That is a false pass, and it is precisely
	// the shape of the 690cec8 harness bug this project keeps re-learning, so the
	// condition that REACHES the refusal is what is asserted.
	if !strings.Contains(body, "if batch.Status != repository.PayoutBatchApproved") {
		t.Errorf("the refusal is not reached, so a draft batch still exports:\\n%s\\n"+
			"an error code inside `if false` is not a control", body)
	}
	// Every settled state is acceptable, not only `approved`: a batch that has
	// already been submitted or paid must still be exportable for the bank's
	// acknowledgement, and refusing would leave an operator unable to prove a
	// transfer happened.
	for _, ok := range []string{"PayoutBatchApproved", "PayoutBatchSubmitted", "PayoutBatchPaid"} {'''
assert s.count(old) == 1
s = s.replace(old, new, 1)

old2 = '''	body := functionSource(t, "payment_service.go", "ApprovePayoutBatch")
	if !strings.Contains(body, "PAYOUT_BATCH_EMPTY") {
		t.Errorf("an empty batch is approvable:\\n%s\\n"+
			"the record would say an operator approved a batch of zero withdrawals", body)
	}'''
new2 = '''	body := functionSource(t, "payment_service.go", "ApprovePayoutBatch")
	// The CONDITION, for the same reason as M39: the error code survives in dead
	// code, so asserting its presence proves nothing about whether the refusal can
	// ever fire.
	if !strings.Contains(body, "if items == 0 {") {
		t.Errorf("an empty batch is approvable:\\n%s\\n"+
			"the record would say an operator approved a batch of zero withdrawals. "+
			"(Asserting the error code instead of this condition passes on `if "+
			"false` -- see M39)", body)
	}
	// And the guard must run BEFORE the commit, or the tx rolls the refusal back.
	commit := strings.Index(body, "tx.Commit(ctx)")
	guard := strings.Index(body, "if items == 0 {")
	if guard < 0 || commit < 0 || guard > commit {
		t.Errorf("the empty-batch guard does not precede the commit:\\n%s\\n"+
			"a guard after the commit is a guard that does not stop anything", body)
	}'''
assert s.count(old2) == 1
s = s.replace(old2, new2, 1)

io.open(p, 'w', encoding='utf-8', newline='\n').write(s)

# --- 2. M40's anchor had one tab too many ---
p2 = 'cmd/mutation-check/main.go'
t = io.open(p2, encoding='utf-8').read()
old3 = '\t\t\told:   "\\t\\t\\t   SET total = COALESCE(agg.sum, 0),",'
new3 = '\t\t\told:   "\\t\\t   SET total = COALESCE(agg.sum, 0),",'
assert t.count(old3) == 1, t.count(old3)
t = t.replace(old3, new3, 1)
old4 = '\t\t\tnew: "\\t\\t\\t   SET total = COALESCE(agg.sum, 0) + 1,",'
new4 = '\t\t\tnew: "\\t\\t   SET total = COALESCE(agg.sum, 0) + 1,",'
assert t.count(old4) == 1, t.count(old4)
t = t.replace(old4, new4, 1)
io.open(p2, 'w', encoding='utf-8', newline='\n').write(t)
print('ok')
