package cmd

import (
        "fmt"
        "os"
        "os/exec"
        "time"

        "github.com/ethereum/go-ethereum/common"
        "github.com/ethereum/go-ethereum/common/hexutil"
        "github.com/ethereum/go-ethereum/crypto"
        "github.com/ethereum/go-ethereum/log"
        "github.com/urfave/cli/v2"

        "github.com/pkg/profile"

        "github.com/ethereum-optimism/cannon/mipsevm"
        "github.com/ethereum-optimism/cannon/preimage"
)

var (
        RunInputFlag = &cli.PathFlag{
                Name:      "input",
                Usage:     "path of input JSON state. Stdin if left empty.",
                TakesFile: true,
                Value:     "state.json",
                Required:  true,
        }
        RunOutputFlag = &cli.PathFlag{
                Name:      "output",
                Usage:     "path of output JSON state. Stdout if left empty.",
                TakesFile: true,
                Value:     "out.json",
                Required:  false,
        }
        patternHelp    = "'never' (default), 'always', '=123' at exactly step 123, '%123' for every 123 steps"
        RunProofAtFlag = &cli.GenericFlag{
                Name:     "proof-at",
                Usage:    "step pattern to output proof at: " + patternHelp,
                Value:    new(StepMatcherFlag),
                Required: false,
        }
        RunProofFmtFlag = &cli.StringFlag{
                Name:     "proof-fmt",
                Usage:    "format for proof data output file names. Proof data is written to stdout if empty.",
                Value:    "proof-%d.json",
                Required: false,
        }
        RunSnapshotAtFlag = &cli.GenericFlag{
                Name:     "snapshot-at",
                Usage:    "step pattern to output snapshots at: " + patternHelp,
                Value:    new(StepMatcherFlag),
                Required: false,
        }
        RunSnapshotFmtFlag = &cli.StringFlag{
                Name:     "snapshot-fmt",
                Usage:    "format for snapshot output file names.",
                Value:    "state-%d.json",
                Required: false,
        }
        RunStopAtFlag = &cli.GenericFlag{
                Name:     "stop-at",
                Usage:    "step pattern to stop at: " + patternHelp,
                Value:    new(StepMatcherFlag),
                Required: false,
        }
        RunMetaFlag = &cli.PathFlag{
                Name:     "meta",
                Usage:    "path to metadata file for symbol lookup for enhanced debugging info durign execution.",
                Value:    "meta.json",
                Required: false,
        }
        RunInfoAtFlag = &cli.GenericFlag{
                Name:     "info-at",
                Usage:    "step pattern to print info at: " + patternHelp,
                Value:    MustStepMatcherFlag("%100000"),
                Required: false,
        }
        RunOPCAtFlag = &cli.GenericFlag{
                Name:     "opc-at",
                Usage:    "step pattern to print info at: " + patternHelp,
                Value:    MustStepMatcherFlag("%100000"),
                Required: false,
        }
        RunPProfCPU = &cli.BoolFlag{
                Name:  "pprof.cpu",
                Usage: "enable pprof cpu profiling",
        }
)

type Proof struct {
        Step uint64 `json:"step"`

        Pre  common.Hash `json:"pre"`
        Post common.Hash `json:"post"`

        StepInput   hexutil.Bytes `json:"step-input"`
        OracleInput hexutil.Bytes `json:"oracle-input"`
}

type rawHint string

func (rh rawHint) Hint() string {
        return string(rh)
}

type rawKey [32]byte

func (rk rawKey) PreimageKey() [32]byte {
        return rk
}

type ProcessPreimageOracle struct {
        pCl *preimage.OracleClient
        hCl *preimage.HintWriter
        cmd *exec.Cmd
}

func NewProcessPreimageOracle(name string, args []string) (*ProcessPreimageOracle, error) {
        if name == "" {
                return &ProcessPreimageOracle{}, nil
        }

        pClientRW, pOracleRW, err := preimage.CreateBidirectionalChannel()
        if err != nil {
                return nil, err
        }
        hClientRW, hOracleRW, err := preimage.CreateBidirectionalChannel()
        if err != nil {
                return nil, err
        }

        cmd := exec.Command(name, args...)
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        cmd.ExtraFiles = []*os.File{
                hOracleRW.Reader(),
                hOracleRW.Writer(),
                pOracleRW.Reader(),
                pOracleRW.Writer(),
        }
        out := &ProcessPreimageOracle{
                pCl: preimage.NewOracleClient(pClientRW),
                hCl: preimage.NewHintWriter(hClientRW),
                cmd: cmd,
        }
        return out, nil
}

func (p *ProcessPreimageOracle) Hint(v []byte) {
        if p.hCl == nil { // no hint processor
                return
        }
        p.hCl.Hint(rawHint(v))
}

func (p *ProcessPreimageOracle) GetPreimage(k [32]byte) []byte {
        if p.pCl == nil {
                panic("no pre-image retriever available")
        }
        return p.pCl.Get(rawKey(k))
}

func (p *ProcessPreimageOracle) Start() error {
        if p.cmd == nil {
                return nil
        }
        return p.cmd.Start()
}

func (p *ProcessPreimageOracle) Close() error {
        if p.cmd == nil {
                return nil
        }
        _ = p.cmd.Process.Signal(os.Interrupt)
        p.cmd.WaitDelay = time.Second * 10
        err := p.cmd.Wait()
        if err, ok := err.(*exec.ExitError); ok {
                if err.Success() {
                        return nil
                }
        }
        return err
}

type StepFn func(proof bool) (*mipsevm.StepWitness, error)

func Guard(proc *os.ProcessState, fn StepFn) StepFn {
        return func(proof bool) (*mipsevm.StepWitness, error) {
                wit, err := fn(proof)
                if err != nil {
                        if proc.Exited() {
                                return nil, fmt.Errorf("pre-image server exited with code %d, resulting in err %v", proc.ExitCode(), err)
                        } else {
                                return nil, err
                        }
                }
                return wit, nil
        }
}

var _ mipsevm.PreimageOracle = (*ProcessPreimageOracle)(nil)



var rmmcode = map[uint32]string{
        0:  "bltz",
        1:  "bgez",
        2:  "bltzl",
        3:  "bgezl",
        17: "bgezal",
}

func PrintRMMcode(state * mipsevm.State, inst uint32) string {
        rs := (inst >> 21) & 0x1f
        fun := (inst >> 16) & 0x1f
        offset := inst & 0x0ffff
        opc := rmmcode[fun]
        switch opc {
        case "bgez", "bgezal", "bltz", "bltzl", "bgezl":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rs), offset)
        default:
                return fmt.Sprintf("err rmm inst:%d,%d,%s, %d\n", 1, fun, opc, inst)
        }
}

var s28code = map[uint32]string{
        0:  "madd",
        1:  "maddu",
        2:  "mul",
        4:  "msub",
        5:  "msubu",
        32: "clz",
        33: "clo",
}

func PrintS28code(state * mipsevm.State, inst uint32) string {
        fun := inst & 0x3f
        opc := s28code[fun]
        rs := (inst >> 21) & 0x1f
        rt := (inst >> 16) & 0x1f
        rd := (inst >> 11) & 0x1f
        switch opc {
        case "madd", "maddu", "msub", "msubu":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rs), ReadReg(state, rt))
        case "mul":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rs), ReadReg(state, rt))
        case "clz":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rs))
        default:
                return fmt.Sprintf("err S28 inst:%d,%d,%s, %d\n", 28, fun, opc, inst)
        }
}

var rcode = map[uint32]string{
        0:  "sll",
        2:  "srl",
        3:  "sra",
        4:  "sllv",
        6:  "srlv",
        7:  "srav",
        8:  "jr",
        9:  "jalr",
        10: "movz",
        11: "movn",
        12: "syscall",
        15: "sync",
        16: "mfhi",
        17: "mthi",
        18: "mflo",
        19: "mtlo",
        24: "mult",
        25: "multu",
        26: "div",
        27: "divu",
        32: "add",
        33: "addu",
        34: "sub",
        35: "subu",
        36: "and",
        37: "or",
        38: "xor",
        39: "nor",
        42: "slt",
        43: "sltu",
}

func PrintRcode(state * mipsevm.State, inst uint32) string {
        fun := inst & 0x3f
        opc := rcode[fun]
        rs := (inst >> 21) & 0x1f
        rt := (inst >> 16) & 0x1f
        rd := (inst >> 11) & 0x1f
        shamt := (inst >> 6) & 0x1f
        switch opc {
        case "sll", "srl", "sra":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rt), shamt)
        case "sllv", "srlv", "srav":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rt), ReadReg(state, rs))
        case "jr":
                return fmt.Sprintf("%s %d\n", opc, ReadReg(state, rs))
        case "jalr":
                if ReadReg(state, rd) != 31 {
                        return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rs))
                } else {
                        return fmt.Sprintf("%s %d\n", opc, ReadReg(state, rs))
                }
        case "syscall":
                return fmt.Sprintf("%s\n", opc)
        case "sync":
                return fmt.Sprintf("%s %d\n", opc, shamt)
        case "mfhi", "mflo":
                return fmt.Sprintf("%s %d\n", opc, ReadReg(state, rd))
        case "mthi":
        case "mtlo":
                return fmt.Sprintf("%s %d\n", opc, ReadReg(state, rs))
        case "mult", "multu", "div", "divu":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rs), ReadReg(state, rt))
        case "add", "addu", "sub", "subu", "and", "or", "xor", "nor", "slt", "sltu", "movz", "movn":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rd), ReadReg(state, rs), ReadReg(state, rt))
        default:
                return fmt.Sprintf("err R inst:%d,%d,%s, %d\n", 0, fun, opc, inst)
        }
        return fmt.Sprintf("err R inst:%d,%d,%s, %d\n", 0, fun, opc, inst)
}

var jcode = map[uint32]string{
        2: "j",
        3: "jal",
}

func PrintJcode(state * mipsevm.State, inst uint32) string {
        op := inst >> 26
        opc := jcode[op]
        address := inst & 0x03ffffff
        switch opc {
        case "j", "jal":
                return fmt.Sprintf("%s %d\n", opc, address)
        default:
                return fmt.Sprintf("err J inst:%d,%s, %d\n", op, opc, inst)
        }
}

var icode = map[uint32]string{
        4:  "beq",
        5:  "bne",
        6:  "blez",
        7:  "bgtz",
        8:  "addi",
        9:  "addiu",
        10: "slti",
        11: "sltiu",
        12: "andi",
        13: "ori",
        14: "xori",
        15: "lui",
        32: "lb",
        33: "lh",
        34: "lwl",
        35: "lw",
        36: "lbu",
        37: "lhu",
        38: "lwr",
        40: "sb",
        41: "sh",
        42: "swl",
        43: "sw",
        46: "swr",
        48: "ll",
        56: "sc",
}

func PrintIcode(state * mipsevm.State, inst uint32) string {
        op := inst >> 26
        opc := icode[op]
        rs := (inst >> 21) & 0x1f
        rt := (inst >> 16) & 0x1f
        imm := inst & 0x0ffff
        switch opc {
        case "beq", "bne":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rs), ReadReg(state, rt), imm)
        case "blez", "bgtz":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rs), imm)
        case "addi", "addiu", "slti", "sltiu", "andi", "ori", "xori":
                return fmt.Sprintf("%s %d, %d, %d\n", opc, ReadReg(state, rt), ReadReg(state, rs), imm)
        case "lui", "lwr", "swl", "swr":
                return fmt.Sprintf("%s %d, %d\n", opc, ReadReg(state, rt), imm)
        case "lb", "lh", "lwl", "lw", "lbu", "lhu", "sb", "sh", "sw", "ll", "sc":
                return fmt.Sprintf("%s %d, %d (%d)\n", opc, ReadReg(state, rt), imm, ReadReg(state, rs))
        default:
                return fmt.Sprintf("err I inst:%d,%s, %d\n", op, opc, inst)
        }
}

func ReadReg(state * mipsevm.State, r uint32) uint32 {
        return state.Registers[r]
}

func PrintOPCode(state * mipsevm.State, inst uint32, op uint32) string {
        if op == 0 {
                return PrintRcode(state, inst)
        } else if op == 1 {
                return PrintRMMcode(state, inst)
        } else if op == 2 || op == 3 {
                return PrintJcode(state, inst)
        } else if op == 28 {
                return PrintS28code(state, inst)
        } else if op > 3 {
                return PrintIcode(state, inst)
        } else {
                return fmt.Sprintf("Op err:%d,%d\n", op, inst)
        }
}


func Run(ctx *cli.Context) error {
        if ctx.Bool(RunPProfCPU.Name) {
                defer profile.Start(profile.NoShutdownHook, profile.ProfilePath("."), profile.CPUProfile).Stop()
        }

        state, err := loadJSON[mipsevm.State](ctx.Path(RunInputFlag.Name))
        if err != nil {
                return err
        }

        l := Logger(os.Stderr, log.LvlInfo)
        outLog := &mipsevm.LoggingWriter{Name: "program std-out", Log: l}
        errLog := &mipsevm.LoggingWriter{Name: "program std-err", Log: l}

        // split CLI args after first '--'
        args := ctx.Args().Slice()
        for i, arg := range args {
                if arg == "--" {
                        args = args[i+1:]
                        break
                }
        }
        if len(args) == 0 {
                args = []string{""}
        }

        po, err := NewProcessPreimageOracle(args[0], args[1:])
        if err != nil {
                return fmt.Errorf("failed to create pre-image oracle process: %w", err)
        }
        if err := po.Start(); err != nil {
                return fmt.Errorf("failed to start pre-image oracle server: %w", err)
        }
        defer func() {
                if err := po.Close(); err != nil {
                        l.Error("failed to close pre-image server", "err", err)
                }
        }()

        stopAt := ctx.Generic(RunStopAtFlag.Name).(*StepMatcherFlag).Matcher()
        proofAt := ctx.Generic(RunProofAtFlag.Name).(*StepMatcherFlag).Matcher()
        snapshotAt := ctx.Generic(RunSnapshotAtFlag.Name).(*StepMatcherFlag).Matcher()
        infoAt := ctx.Generic(RunInfoAtFlag.Name).(*StepMatcherFlag).Matcher()
        opcAt := ctx.Generic(RunOPCAtFlag.Name).(*StepMatcherFlag).Matcher()

        var meta *mipsevm.Metadata
        if metaPath := ctx.Path(RunMetaFlag.Name); metaPath == "" {
                l.Info("no metadata file specified, defaulting to empty metadata")
                meta = &mipsevm.Metadata{Symbols: nil} // provide empty metadata by default
        } else {
                if m, err := loadJSON[mipsevm.Metadata](metaPath); err != nil {
                        return fmt.Errorf("failed to load metadata: %w", err)
                } else {
                        meta = m
                }
        }

        us := mipsevm.NewInstrumentedState(state, po, outLog, errLog)
        proofFmt := ctx.String(RunProofFmtFlag.Name)
        snapshotFmt := ctx.String(RunSnapshotFmtFlag.Name)

        stepFn := us.Step
        if po.cmd != nil {
                stepFn = Guard(po.cmd.ProcessState, stepFn)
        }

        start := time.Now()
        startStep := state.Step

        // avoid symbol lookups every instruction by preparing a matcher func
        sleepCheck := meta.SymbolMatcher("runtime.notesleep")

        for !state.Exited {
                if state.Step%100 == 0 { // don't do the ctx err check (includes lock) too often
                        if err := ctx.Context.Err(); err != nil {
                                return err
                        }
                }

                step := state.Step

                if opcAt(state){
                        inst := state.Memory.GetMemory(state.PC)
                        opc := inst >> 26

                        logString := PrintOPCode(state,inst,opc)
                        fmt.Printf(logString)
                }
                if infoAt(state) {
                        delta := time.Since(start)
                        l.Info("processing",
                                "step", step,
                                "pc", mipsevm.HexU32(state.PC),
                                "insn", mipsevm.HexU32(state.Memory.GetMemory(state.PC)),
                                "ips", float64(step-startStep)/(float64(delta)/float64(time.Second)),
                                "pages", state.Memory.PageCount(),
                                "mem", state.Memory.Usage(),
                                "name", meta.LookupSymbol(state.PC),
                        )
                }

                if sleepCheck(state.PC) { // don't loop forever when we get stuck because of an unexpected bad program
                        return fmt.Errorf("got stuck in Go sleep at step %d", step)
                }

                if stopAt(state) {
                        break
                }

                if snapshotAt(state) {
                        if err := writeJSON[*mipsevm.State](fmt.Sprintf(snapshotFmt, step), state, false); err != nil {
                                return fmt.Errorf("failed to write state snapshot: %w", err)
                        }
                }

                if proofAt(state) {
                        preStateHash := crypto.Keccak256Hash(state.EncodeWitness())
                        witness, err := stepFn(true)
                        if err != nil {
                                return fmt.Errorf("failed at proof-gen step %d (PC: %08x): %w", step, state.PC, err)
                        }
                        postStateHash := crypto.Keccak256Hash(state.EncodeWitness())
                        proof := &Proof{
                                Step:      step,
                                Pre:       preStateHash,
                                Post:      postStateHash,
                                StepInput: witness.EncodeStepInput(),
                        }
                        if witness.HasPreimage() {
                                proof.OracleInput, err = witness.EncodePreimageOracleInput()
                                if err != nil {
                                        return fmt.Errorf("failed to encode pre-image oracle input: %w", err)
                                }
                        }
                        if err := writeJSON[*Proof](fmt.Sprintf(proofFmt, step), proof, true); err != nil {
                                return fmt.Errorf("failed to write proof data: %w", err)
                        }
                } else {
                        _, err = stepFn(false)
                        if err != nil {
                                return fmt.Errorf("failed at step %d (PC: %08x): %w", step, state.PC, err)
                        }
                }
        }

        if err := writeJSON[*mipsevm.State](ctx.Path(RunOutputFlag.Name), state, true); err != nil {
                return fmt.Errorf("failed to write state output: %w", err)
        }
        return nil
}

var RunCommand = &cli.Command{
        Name:        "run",
        Usage:       "Run VM step(s) and generate proof data to replicate onchain.",
        Description: "Run VM step(s) and generate proof data to replicate onchain. See flags to match when to output a proof, a snapshot, or to stop early.",
        Action:      Run,
        Flags: []cli.Flag{
                RunInputFlag,
                RunOutputFlag,
                RunProofAtFlag,
                RunProofFmtFlag,
                RunSnapshotAtFlag,
                RunSnapshotFmtFlag,
                RunStopAtFlag,
                RunMetaFlag,
                RunInfoAtFlag,
                RunOPCAtFlag,
                RunPProfCPU,
        },
}
