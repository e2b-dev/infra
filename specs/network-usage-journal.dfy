// SUP-898: abstract state transitions; Go refinement is checked by replay tests.
const Max: nat := 18446744073709551615
datatype State = S(sequence: nat, tx: nat, rx: nat, valid: bool, failed: bool, closed: bool)
datatype Kind = Sample | Gap | Close
datatype Action = A(kind: Kind, tx: nat, rx: nat, durable: bool, clockOK: bool)

predicate Inv(s: State) { s.sequence <= Max && s.tx <= Max && s.rx <= Max }
predicate Input(a: Action) { a.tx <= Max && a.rx <= Max }
function Initial(): State { S(0, 0, 0, true, false, false) }
function Overflow(s: State, a: Action): bool {
  a.kind == Sample && (s.tx + a.tx > Max || s.rx + a.rx > Max)
}

function Step(s: State, a: Action): State
  requires Inv(s) && Input(a)
  ensures Inv(Step(s, a))
  ensures !s.valid ==> !Step(s, a).valid
  ensures s.closed ==> Step(s, a) == s
{
  if s.closed then s
  else if s.failed then S(s.sequence, s.tx, s.rx, s.valid, true, a.kind == Close)
  else if s.sequence == Max || !a.durable then
    S(s.sequence, s.tx, s.rx, s.valid, true, a.kind == Close)
  else
    S(s.sequence + 1,
      if a.kind == Sample && !Overflow(s, a) then s.tx + a.tx else s.tx,
      if a.kind == Sample && !Overflow(s, a) then s.rx + a.rx else s.rx,
      s.valid && a.clockOK && a.kind != Gap && !Overflow(s, a),
      false, a.kind == Close)
}

lemma DurableConservation(s: State, a: Action)
  requires Inv(s) && Input(a)
  requires !s.failed && !s.closed && s.sequence < Max && a.durable
  requires a.kind == Sample && !Overflow(s, a)
  ensures Step(s, a).tx == s.tx + a.tx
  ensures Step(s, a).rx == s.rx + a.rx
  ensures Step(s, a).sequence == s.sequence + 1
{}

lemma NoPublicationOnFailure(s: State, a: Action)
  requires Inv(s) && Input(a)
  requires !a.durable || s.sequence == Max || s.failed || s.closed
  ensures Step(s, a).sequence == s.sequence
  ensures Step(s, a).tx == s.tx && Step(s, a).rx == s.rx
{}

lemma OverflowNeverWraps(s: State, a: Action)
  requires Inv(s) && Input(a) && Overflow(s, a)
  ensures Step(s, a).tx == s.tx && Step(s, a).rx == s.rx
  ensures !s.failed && !s.closed && s.sequence < Max && a.durable ==> !Step(s, a).valid
{}

function Replay(s: State, actions: seq<Action>): State
  requires Inv(s)
  requires forall a <- actions :: Input(a)
  ensures Inv(Replay(s, actions))
  ensures !s.valid ==> !Replay(s, actions).valid
  decreases |actions|
{
  if |actions| == 0 then s else Replay(Step(s, actions[0]), actions[1..])
}

// Executable oracle: each trace starts at Initial. Columns contain the input
// as well as expected output; Go tests consume this output without duplicating
// the action list. JSON booleans and uint64 decimal values are emitted as CSV.
method Emit(trace: nat, actions: seq<Action>)
  requires forall a <- actions :: Input(a)
{
  var s := Initial();
  for i := 0 to |actions|
    invariant Inv(s)
  {
    var a := actions[i];
    s := Step(s, a);
    print trace, ",", (if a.kind == Sample then "sample" else if a.kind == Gap then "gap" else "close"), ",",
      a.tx, ",", a.rx, ",", a.durable, ",", a.clockOK, ",",
      s.sequence, ",", s.tx, ",", s.rx, ",", s.valid, ",", s.failed, ",", s.closed, "\n";
  }
}

method Main() {
  Emit(0, [A(Sample, 9007199254740993, 3, true, true), A(Sample, 9007199254740993, 3, true, true),
    A(Gap, 0, 0, true, true), A(Sample, 0, 7, true, true), A(Close, 0, 0, true, true)]);
  Emit(1, [A(Sample, Max, 0, true, true), A(Sample, 1, 1, true, true), A(Sample, 0, 1, true, true), A(Close, 0, 0, true, true)]);
  Emit(2, [A(Sample, 0, Max, true, true), A(Sample, 1, 1, true, true), A(Close, 0, 0, true, true)]);
  Emit(3, [A(Sample, 2, 3, true, true), A(Sample, 4, 5, false, true), A(Sample, 1, 1, true, true), A(Close, 0, 0, true, true)]);
  Emit(4, [A(Sample, 2, 3, true, true), A(Sample, 4, 5, true, false), A(Sample, 0, 0, true, true), A(Close, 0, 0, true, true)]);
  Emit(5, [A(Close, 0, 0, false, true), A(Sample, 1, 1, true, true)]);
}
