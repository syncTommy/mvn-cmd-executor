package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	mvn_build_failure  = "BUILD FAILURE"
	mvn_build_success  = "BUILD SUCCESS"
	mvn_unknown        = "MVN UNKNOWN"
	defaultRetry       = 5
	defaultConcurrency = 6
	defaultCmd         = "mvn clean" // 默认命令
)

var (
	cmdStr      string
	parentDir   string
	retryTimes  int
	concurrency int
)

func init() {
	flag.StringVar(&cmdStr, "cmd", "", "The command to execute in each Maven project directory")
	flag.StringVar(&parentDir, "dir", ".", "The parent directory containing the Maven projects (absolute path)")
	flag.IntVar(&retryTimes, "retry", defaultRetry, "Number of retries for failed commands")
	flag.IntVar(&concurrency, "concurrency", defaultConcurrency, "Maximum number of concurrent goroutines")
	flag.Parse()

	// Ensure parentDir is an absolute path
	if parentDir == "" || parentDir == "." {
		var err error
		parentDir, err = os.Getwd()
		if err != nil {
			fmt.Printf("Error getting current working directory: %v\n", err)
			os.Exit(1)
		}
	} else {
		parentDir, _ = filepath.Abs(parentDir)
	}

	// 如果用户没有提供 cmd 参数，则使用默认命令 "mvn clean"
	if cmdStr == "" {
		cmdStr = defaultCmd
		fmt.Println("No command provided. Using default command:", cmdStr)
	}
}

// findMavenProjects 接收一个目录路径，返回一个字符串切片，包含所有包含 pom.xml 文件的子目录名。
func findMavenProjects(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var mavenProjects []string
	for _, file := range files {
		if file.IsDir() {
			pomPath := filepath.Join(dir, file.Name(), "pom.xml")
			if _, err := os.Stat(pomPath); err == nil {
				mavenProjects = append(mavenProjects, file.Name())
			}
		}
	}
	fmt.Printf("Found Maven projects:\n\n%s\n", strings.Join(mavenProjects, "\n"))
	fmt.Printf("\nMaven projects total count : %d\n", len(mavenProjects))
	return mavenProjects, nil
}

// runCommandInDir 在指定目录中执行命令，并返回输出和错误信息。
func runCommandInDir(dir, cmd string) (string, error) {
	fmt.Println("dir = ", dir, " , Running command : ", cmd)
	command := exec.Command("sh", "-c", cmd)
	command.Dir = dir
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %v, stderr: %v", err, stderr.String())
	}
	fmt.Println("dir = ", dir, " , Done !")
	return out.String(), nil
}

type dirCmdInfo struct {
	dir        string
	rawInfo    string
	simpleInfo string
	errInfo    string
}

type DirCmdRes struct {
	infos      []dirCmdInfo
	conclusion string
	failedDirs []string
}

func RunCommandInDirBatchAndPrint(cmd string, timeout time.Duration) (err error) {
	res := RunCommandInDirBatchWithRetry(cmd, timeout)
	cmdFileName := sanitizeCmdForFileName(cmd)
	curTimeStr := getCurrentDateTimeString()
	rawFileName := "./" + cmdFileName + curTimeStr + "-raw.log"
	simpleFileName := "./" + cmdFileName + curTimeStr + "-simple.log"
	errFileName := "./" + cmdFileName + curTimeStr + "-err.log"

	// 写入日志文件
	err = createAndWriteFile(rawFileName, res.conclusion, res.infos, func(info dirCmdInfo) string { return info.rawInfo })
	if err != nil {
		return err
	}

	err = createAndWriteFile(simpleFileName, res.conclusion, res.infos, func(info dirCmdInfo) string { return info.simpleInfo })
	if err != nil {
		return err
	}

	err = createAndWriteFile(errFileName, res.conclusion, res.infos, func(info dirCmdInfo) string { return info.errInfo })
	if err != nil {
		return err
	}

	// 如果有失败的目录，写入日志
	if len(res.failedDirs) > 0 {
		failedDirsStr := "=============== FAILED DIRS ===============\n"
		for _, dir := range res.failedDirs {
			failedDirsStr += dir + "\n"
		}
		failedDirsStr += "=============== FAILED DIRS ===============\n"

		err = createAndWriteFile(rawFileName, failedDirsStr, nil, nil)
		if err != nil {
			return err
		}

		err = createAndWriteFile(simpleFileName, failedDirsStr, nil, nil)
		if err != nil {
			return err
		}

		err = createAndWriteFile(errFileName, failedDirsStr, nil, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

func RunCommandInDirBatchWithRetry(cmd string, timeout time.Duration) DirCmdRes {
	start := time.Now()
	projects, err := findMavenProjects(parentDir)
	if err != nil {
		return DirCmdRes{conclusion: fmt.Sprintf("Error finding Maven projects: %v\n", err)}
	}

	var totalInfoMap = make(map[string]dirCmdInfo)
	var failedDirs []string

	for retry := 0; retry < retryTimes; retry++ {
		fmt.Println("retryTimes = ", retry)
		processingDirs := make([]string, 0, len(projects))
		for _, dir := range projects {
			if _, ok := totalInfoMap[dir]; !ok {
				processingDirs = append(processingDirs, dir)
			}
		}

		if len(processingDirs) == 0 {
			break
		}

		infos, _ := runCommandInDirBatch(processingDirs, cmd, timeout)
		for d, i := range infos {
			if i.errInfo == "" {
				totalInfoMap[d] = i
			} else {
				if retry == retryTimes-1 || time.Since(start) >= timeout {
					totalInfoMap[d] = i
					failedDirs = append(failedDirs, d)
				}
			}
		}

		if time.Since(start) >= timeout {
			break
		}
	}

	// 计算统计信息
	var notExecutedCnt, executedCnt, erroredCnt, succeededCnt int
	unfinishedStr := "=============== UNFINISHED DIRS (TIME OUT / RETRIED ENOUGH) ===============\n"
	for _, dir := range projects {
		if val, ok := totalInfoMap[dir]; !ok {
			unfinishedStr += dir + "\n"
			notExecutedCnt++
		} else {
			executedCnt++
			if val.errInfo != "" {
				erroredCnt++
			} else {
				succeededCnt++
			}
		}
	}
	unfinishedStr += "=============== UNFINISHED DIRS (TIME OUT / RETRIED ENOUGH) ===============\n"

	timeCostStr := "=============== TIME COST ===============\n" +
		genTimeCostStr(start, time.Now()) +
		"\n=============== TIME COST ===============\n"

	conclusion := unfinishedStr +
		"**************************** CONCLUSION ****************************\n" +
		fmt.Sprintf("total dirs : %d =  notExecutedCnt : %d, executedCnt: %d (erroredCnt : %d + succeededCnt : %d )\n",
			len(projects), notExecutedCnt, executedCnt, erroredCnt, succeededCnt) +
		"**************************** CONCLUSION ****************************\n" +
		timeCostStr

	// 将结果转换为 DirCmdRes
	res := DirCmdRes{
		infos:      make([]dirCmdInfo, 0, len(totalInfoMap)),
		conclusion: conclusion,
		failedDirs: failedDirs,
	}
	for _, dir := range projects {
		if val, ok := totalInfoMap[dir]; ok {
			res.infos = append(res.infos, val)
		}
	}

	return res
}

func runCommandInDirBatch(dirs []string, cmd string, timeout time.Duration) (map[string]dirCmdInfo, error) {
	ticker := time.NewTicker(timeout).C

	var wg sync.WaitGroup
	wg.Add(len(dirs))

	var doneChan = make(chan struct{})
	go func() {
		wg.Wait()
		close(doneChan)
	}()

	var cmdOutChan = make(chan dirCmdInfo, len(dirs))

	var limitCh = make(chan struct{}, concurrency)

	for _, dir := range dirs {
		dir := dir
		go func() {
			defer func() {
				wg.Done()
				<-limitCh
			}()
			limitCh <- struct{}{}
			outputStr := cmd + "----->" + dir + ":\n"
			simpleStr := outputStr

			localOutput, localErr := runCommandInDir(filepath.Join(parentDir, dir), cmd)
			fmt.Println("dir = ", dir, ", finished!")
			if localErr != nil {
				outputStr += localErr.Error()
				simpleStr += localErr.Error()
			} else {
				outputStr += localOutput
				simpleStr += extractBuildResult(localOutput)
			}
			outputStr += "\n================================================================================================\n"
			simpleStr += "\n================================================================================================\n"

			info := dirCmdInfo{
				dir:        dir,
				rawInfo:    outputStr,
				simpleInfo: simpleStr,
			}
			if localErr != nil {
				info.errInfo = outputStr
			}
			cmdOutChan <- info
		}()
	}

	select {
	case <-ticker:
		break
	case <-doneChan:
		break
	}

	dir2InfoMap := make(map[string]dirCmdInfo)
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		for {
			select {
			case res := <-cmdOutChan:
				dir2InfoMap[res.dir] = res
			default:
				wg2.Done()
				return
			}
		}
	}()
	wg2.Wait()
	for _, dir := range dirs {
		if _, ok := dir2InfoMap[dir]; !ok {
			dir2InfoMap[dir] = dirCmdInfo{
				dir:     dir,
				errInfo: "time-out-err",
			}
		}
	}
	return dir2InfoMap, nil
}

func extractBuildResult(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, mvn_build_failure) {
			return mvn_build_failure
		}
		if strings.Contains(line, mvn_build_success) {
			return mvn_build_success
		}
	}
	return mvn_unknown
}

func getCurrentDateTimeString() string {
	return time.Now().Format("2006-01-02-15-04-05")
}

func genTimeCostStr(start, end time.Time) string {
	timeCostSecTotal := int64(end.Sub(start).Seconds())
	timeCostMinute := timeCostSecTotal / 60
	timeCostSecRest := timeCostSecTotal % 60
	return fmt.Sprintf("TIMECOST: %d minutes, %d seconds", timeCostMinute, timeCostSecRest)
}

func createAndWriteFile(filename string, content string, infos []dirCmdInfo, infoFunc func(dirCmdInfo) string) error {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("create file %s error: %v\n", filename, err)
		return err
	}
	defer file.Close()

	if content != "" {
		_, err = file.WriteString(content)
		if err != nil {
			fmt.Printf("write %s error: %v\n", filename, err)
			return err
		}
	}

	if infos != nil && infoFunc != nil {
		for _, info := range infos {
			_, err = file.WriteString(infoFunc(info))
			if err != nil {
				fmt.Printf("write %s error: %v\n", filename, err)
				return err
			}
		}
	}

	return nil
}

func sanitizeCmdForFileName(cmd string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, cmd)
}

func main() {
	if cmdStr == "" {
		fmt.Println("No command provided. Using default command:", defaultCmd)
		cmdStr = defaultCmd
	}

	err := RunCommandInDirBatchAndPrint(cmdStr, time.Minute*15)
	if err != nil {
		fmt.Println("RunCommandInDirBatchAndPrint err:", err)
		os.Exit(1)
	}
}
