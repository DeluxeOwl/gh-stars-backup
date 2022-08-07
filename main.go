package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/go-github/v45/github"
	"github.com/peterbourgon/ff/v3"
	"golang.org/x/oauth2"
)

func main() {

	log.SetOutput(os.Stdout)

	fs := flag.NewFlagSet("gh-stars-backup", flag.ContinueOnError)
	var (
		dirFormat = fs.String("dir-format", "{{.RepoName}} [{{.RepoAuthor}}]", "go template that specifies the format of git directories")
		ghPAT     = fs.String("gh-pat", "", "github pat token, scope: repo & user")
		limit     = fs.Int("limit", 16, "goroutine limiter for cloning/pulling repos")
		pullArgs  = fs.String("pull-args", "", "arguments for git pull")
		cloneArgs = fs.String("clone-args", "", "arguments for git clone")
		outputDir = fs.String("output-dir", "./", "the directory where the repos will be saved")
	)

	err := ff.Parse(fs, os.Args[1:],
		ff.WithEnvVars(),
	)

	if err != nil && err.Error() == "error parsing commandline arguments: flag: help requested" {
		os.Exit(0)
	}

	if *ghPAT == "" {
		log.Fatalln("must provide github PAT")
	}

	tmpl := template.Must(template.New("repoFormat").Parse(*dirFormat))

	_, lookErr := exec.LookPath("git")
	if lookErr != nil {
		log.Panic(lookErr)
	}

	if _, err := os.Stat(*outputDir); os.IsNotExist(err) && *outputDir != "./" {
		err = os.Mkdir(*outputDir, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
		*outputDir = strings.TrimRight(*outputDir, "/")
	}

	// connect to github
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: *ghPAT,
	})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	opts := &github.ActivityListStarredOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	// get a list of all the starred repositories
	var starredRepos []*github.StarredRepository

	for {
		repos, resp, err := client.Activity.ListStarred(ctx, "", opts)

		switch err.(type) {
		case *github.RateLimitError:
			log.Println("rate limit, sleeping for 60s")
			time.Sleep(time.Minute)
			continue
		case nil:
			break
		default:
			log.Println(err)
		}

		starredRepos = append(starredRepos, repos...)

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	// clone or pull the repos
	wg := &sync.WaitGroup{}
	defer wg.Wait()
	limiter := make(chan struct{}, *limit)

	cloneWithTokenPrefix := "https://" + *ghPAT + "@"

	for _, r := range starredRepos {

		wg.Add(1)
		limiter <- struct{}{}

		go func(r *github.StarredRepository) {
			defer wg.Done()
			defer func() {
				<-limiter
			}()

			ghRepo := r.GetRepository()

			rfn := ghRepo.GetFullName()
			cloneUrl := strings.Replace(ghRepo.GetCloneURL(), "https://", cloneWithTokenPrefix, 1)

			f := strings.Split(rfn, "/")
			author := f[0]
			name := f[1]

			var repoDir bytes.Buffer

			err := tmpl.Execute(&repoDir, struct {
				RepoAuthor string
				RepoName   string
			}{
				RepoAuthor: author,
				RepoName:   name,
			})

			if err != nil {
				log.Panicf("couldn't parse into template: %s %s\n", author, name)
			}

			dir := *outputDir + "/" + repoDir.String()

			if _, err := os.Stat(dir); os.IsNotExist(err) {
				cloneRepo(rfn, cloneUrl, dir, *cloneArgs)
			} else {
				pullRepo(rfn, cloneUrl, dir, *pullArgs)
			}

		}(r)

	}

}

func cloneRepo(repoFullName, cloneUrl, dir, cloneArgs string) {
	start := time.Now()

	var cloneCmd *exec.Cmd

	if cloneArgs == "" {
		cloneCmd = exec.Command("git", "clone", cloneUrl, dir)
	} else {
		splitArgs := strings.Split(cloneArgs, " ")
		splitArgs = append(splitArgs, cloneUrl, dir)
		splitArgs = append([]string{"clone"}, splitArgs...)
		cloneCmd = exec.Command("git", splitArgs...)
	}

	_, err := cloneCmd.Output()

	if err != nil {
		log.Printf("error when cloning %s: %v\n", repoFullName, err)
		return
	}

	since := time.Since(start)

	log.Printf("cloned %s into \"%s\", took %s\n", repoFullName, dir, since)
}

func pullRepo(repoFullName, cloneUrl, dir, pullArgs string) {

	start := time.Now()

	var pullCmd *exec.Cmd

	if pullArgs == "" {
		pullCmd = exec.Command("git", "-C", dir, "pull")
	} else {
		splitArgs := strings.Split(pullArgs, " ")
		splitArgs = append(splitArgs, cloneUrl, dir)
		splitArgs = append([]string{"pull"}, splitArgs...)
		pullCmd = exec.Command("git", splitArgs...)
	}

	out, err := pullCmd.Output()

	if string(out) == "Already up to date.\n" {
		log.Printf("%s is up to date\n", repoFullName)
		return
	}

	if err != nil {
		log.Printf("error when pulling %s: %v\n", repoFullName, err)
		return
	}

	since := time.Since(start)

	log.Printf("pulled %s into \"%s\", took %s\n", repoFullName, dir, since)
}
