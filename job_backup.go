// `jcloud backup` — every named volume the compose files declare, copied somewhere
// that is not this machine (JCT46, the port of Japode's scripts/backup.sh).
//
//   - One job for the whole stack: it walks what `compose config --volumes`
//     reports, so a service is covered by existing and by nothing else.
//   - It never copies a live database byte for byte. SQLite's pages move under the
//     reader and the copy comes back "database disk image is malformed" during the
//     restore that was meant to save the day; an InnoDB directory is the same with a
//     redo log in it. backup.conf names both: a `sqlite` file is taken with VACUUM
//     INTO, a `mysql` volume with a dump inside a transaction, and each copy is asked
//     whether it is whole before it leaves.
//   - It ships to a restic repository, and `restic forget` decides how long copies
//     are kept. Nothing here counts files or parses dates.
//   - It installs nothing: restic and sqlite3 arrive as pinned images.
//   - One service's missing database does not stop the others' volumes: it is
//     reported, skipped, everything else ships, and the run exits 1 at the end.
//
// What proves any of it is `jcloud restore-test`.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// backupNow is the clock the prune hour is read against, swapped by the tests.
var backupNow = time.Now

// mysqlDumpScript runs inside the database container, where the credentials
// already are because the addon says the image reads them; MYSQL_PWD keeps the
// password off the argument list. The service's own user and database, not root:
// the addon gives root a random password so there is no root credential to store.
const mysqlDumpScript = `set -eu
: "${MARIADB_USER:?the container does not carry it}"
: "${MARIADB_PASSWORD:?the container does not carry it}"
: "${MARIADB_DATABASE:?the container does not carry it}"
export MYSQL_PWD="$MARIADB_PASSWORD"
exec mariadb-dump --user="$MARIADB_USER" --single-transaction --quick --databases "$MARIADB_DATABASE"`

// Backup runs the job at root and answers its exit code.
func Backup(root string, run jobRunner, w io.Writer) int {
	p, err := loadJobPlatform(root, run, w)
	if err == nil {
		err = p.refuseUndeployedWithContainers()
	}
	if err == nil {
		err = p.loadVolumes()
	}
	if err == nil {
		err = p.readBackupConf()
	}
	var env []string
	if err == nil {
		env, err = p.resticConfig()
	}
	if err != nil {
		fmt.Fprintf(w, "\n\033[31mfailed: %v\033[0m\n", err)
		return 1
	}

	// Said on every run, where a green log is read: the day a service left out
	// becomes wrong is the day somebody deploys it (JC101).
	if len(p.undeployed) > 0 {
		p.say("not in this snapshot: %s", strings.Join(p.undeployed, " "))
		for _, svc := range p.undeployed {
			p.note("%s has no image pinned: it has never run here, so it holds nothing to copy", svc)
		}
	}

	// Staging. Named volumes and not directories under the root: the containers
	// that write them run as root, and a root-owned tree in the deploy directory is
	// one the next deploy's rsync has to fight over. Emptied rather than recreated:
	// `docker volume rm` fails while any stopped container references a volume.
	for _, v := range []string{p.stagingVolume(), p.mysqlStaging(), p.cacheVolume()} {
		if err := p.run(jobCmd{Name: "docker", Args: []string{"volume", "create", v}}); err != nil {
			fmt.Fprintf(w, "\n\033[31mfailed: %v\033[0m\n", err)
			return 1
		}
	}
	p.run(p.sh([]string{p.stagingVolume() + ":/staging"}, "find /staging -mindepth 1 -delete"))
	p.run(p.sh([]string{p.mysqlStaging() + ":/staging"}, "find /staging -mindepth 1 -delete"))

	staged := p.stageSQLite()
	dumped := p.stageDumps()

	if code := p.ship(env, staged, dumped); code != 0 {
		return code
	}
	if p.failures == 0 {
		fmt.Fprintf(w, "\n\033[32mbacked up\033[0m\n")
		return 0
	}
	// The volumes are in the snapshot and those databases are not, which is neither
	// "backed up" nor "did not run". Non-zero, so the timer's unit is marked failed.
	fmt.Fprintf(w, "\n\033[31mbacked up, with %d database(s) skipped: the snapshot is missing them\033[0m\n", p.failures)
	return 1
}

// stageSQLite takes each `sqlite` file with VACUUM INTO into staging, and answers
// the entries that made it: only those have their live file excluded from the
// snapshot, or a failed one would ship its volume with a hole where its database
// was.
func (p *jobPlatform) stageSQLite() []confEntry {
	var staged []confEntry
	if len(p.sqliteFiles) == 0 {
		return nil
	}
	p.say("snapshotting %d database(s) with VACUUM INTO", len(p.sqliteFiles))
	for _, e := range p.sqliteFiles {
		vol := p.volume(e.key)
		// sqlite3 creates an empty database when the file is not there, and an empty
		// database passes every check downstream, so the file is asserted first. An
		// empty volume and one missing this file are worded apart: a service that
		// never started, against a database that went away.
		if err := p.run(p.sh([]string{vol + ":/vol:ro"}, `test -f "/vol/$1"`, e.path)); err != nil {
			if n := p.fileCount(vol); n == 0 {
				p.fail("backup.conf names %s:/%s and %s is empty: has this service ever started?", e.key, e.path, e.key)
			} else {
				p.fail("backup.conf names %s:/%s and that volume has no such file, though it holds %d other file(s)", e.key, e.path, n)
			}
			continue
		}
		if err := p.run(p.sh([]string{p.stagingVolume() + ":/staging"}, `mkdir -p "/staging/$1/$(dirname "$2")"`, e.key, e.path)); err != nil {
			p.fail("could not make room under staging for %s:/%s", e.key, e.path)
			continue
		}
		// Read-write on purpose: a database in WAL mode cannot be read at all without
		// writing its -shm file, so :ro fails on exactly the databases this is for.
		to := path.Join("/staging", e.key, e.path)
		if err := p.run(p.sqlite([]string{vol + ":/vol", p.stagingVolume() + ":/staging"},
			"sqlite3", path.Join("/vol", e.path), "VACUUM INTO '"+to+"'")); err != nil {
			p.fail("VACUUM INTO failed for %s:/%s", e.key, e.path)
			continue
		}
		// Asked of the copy: what a restore opens is this file, and this is the last
		// moment anything looks at it before it leaves the machine.
		if answer := p.sqliteIntact(p.stagingVolume()+":/staging:ro", to); answer != "" {
			p.fail("the copy of %s:/%s fails integrity_check: %s", e.key, e.path, answer)
			continue
		}
		staged = append(staged, e)
		p.pass("%s:/%s", e.key, e.path)
	}
	return staged
}

// stageDumps dumps each `mysql` volume from its running container into staging.
func (p *jobPlatform) stageDumps() []string {
	var staged []string
	if len(p.mysql) == 0 {
		return nil
	}
	p.say("dumping %d database(s) inside a transaction", len(p.mysql))
	for _, key := range p.mysql {
		// The container and not the volume: a stopped database has no dump to give,
		// which is one service's failure and not the run's.
		pr, pw := io.Pipe()
		done := make(chan error, 1)
		go func() {
			c := p.compose("exec", "-T", key, "sh", "-c", mysqlDumpScript)
			c.Stdout = pw
			err := p.run(c)
			pw.CloseWithError(err)
			done <- err
		}()
		into := p.shIn([]string{p.mysqlStaging() + ":/staging"}, `cat >"/staging/$1.sql"`, key)
		into.Stdin = pr
		err := p.run(into)
		pr.CloseWithError(io.ErrClosedPipe)
		if dumpErr := <-done; err != nil || dumpErr != nil {
			p.fail("backup.conf names %s and no dump came out of that container: is it up?", key)
			continue
		}
		// mariadb-dump writes this line last, so a file carrying it ran to the end;
		// a cut-off one is what a restore finds as a table that stops halfway.
		if err := p.run(p.sh([]string{p.mysqlStaging() + ":/staging"}, `grep -q '^-- Dump completed' "/staging/$1.sql"`, key)); err != nil {
			p.fail("the dump of %s does not end where mariadb-dump ends one: it was cut off", key)
			continue
		}
		size, _ := p.out(p.sh([]string{p.mysqlStaging() + ":/staging"}, `wc -c <"/staging/$1.sql"`, key))
		staged = append(staged, key)
		p.pass("%s (%s bytes)", key, strings.TrimSpace(size))
	}
	return staged
}

// ship runs restic: init when there is no repository yet, the backup with the
// excludes, then retention.
func (p *jobPlatform) ship(env []string, staged []confEntry, dumped []string) int {
	var mounts []string
	for _, key := range p.volumes {
		mounts = append(mounts, p.volume(key)+":"+snapshotRoot+"/"+key+":ro")
	}
	mounts = append(mounts,
		p.stagingVolume()+":"+snapshotRoot+"/"+sqliteKey+":ro",
		p.mysqlStaging()+":"+snapshotRoot+"/"+mysqlKey+":ro")

	p.say("repository: %s", p.env["BACKUP_REPOSITORY"])
	if err := p.run(p.restic(env, mounts, "cat", "config")); err != nil {
		p.note("no repository there yet: initialising")
		if err := p.run(p.restic(env, mounts, "init")); err != nil {
			fmt.Fprintf(p.w, "\n\033[31mfailed: restic init: %v\033[0m\n", err)
			return 1
		}
	}

	// The live databases are excluded and their staged copies are not: shipping
	// both stores the same rows twice and leaves the restore choosing between them.
	// A dumped volume goes whole, since its directory belongs to an instant nothing
	// recorded.
	var excludes []string
	for _, e := range staged {
		at := snapshotRoot + "/" + e.key + "/" + e.path
		excludes = append(excludes, "--exclude", at, "--exclude", at+"-wal", "--exclude", at+"-shm")
	}
	for _, e := range p.excludes {
		excludes = append(excludes, "--exclude", snapshotRoot+"/"+e.key+"/"+e.path)
	}
	for _, key := range dumped {
		excludes = append(excludes, "--exclude", snapshotRoot+"/"+key)
	}

	p.say("backing up %d volume(s)", len(p.volumes))
	for _, key := range p.volumes {
		p.note("%s", key)
	}
	// --host, rather than the container's random hostname: it is what a restore
	// filters on, and a snapshot filed under a container id belongs to nobody.
	args := append([]string{"backup", snapshotRoot, "--host", p.project, "--tag", p.project}, excludes...)
	c := p.restic(env, mounts, args...)
	c.Stdout = p.w
	if err := p.run(c); err != nil {
		fmt.Fprintf(p.w, "\n\033[31mfailed: restic backup: %v\033[0m\n", err)
		return 1
	}

	// Retention is one line, and restic's. --keep-hourly is what makes running
	// more than once a day mean anything: without it the 09:20 run deletes the
	// 03:20 one and the recovery point stays a day however often the timer fires.
	p.say("retention")
	keep := func(k, def string) string {
		if v := p.env[k]; v != "" {
			return v
		}
		return def
	}
	forget := []string{"forget", "--host", p.project, "--tag", p.project,
		"--keep-hourly", keep("BACKUP_KEEP_HOURLY", "8"), "--keep-daily", keep("BACKUP_KEEP_DAILY", "7"),
		"--keep-weekly", keep("BACKUP_KEEP_WEEKLY", "4"), "--keep-monthly", keep("BACKUP_KEEP_MONTHLY", "6")}
	// --prune repacks under an exclusive lock and is the expensive half, so it runs
	// in one hour of the day, the quiet one; empty prunes every run, which a machine
	// taking one backup a day wants. A run by hand outside that hour does not prune.
	hour := p.env["BACKUP_PRUNE_HOUR"]
	if hour == "" || hourEq(backupNow(), hour) {
		forget = append(forget, "--prune")
		p.note("pruning: this run repacks what earlier runs left unreferenced")
	} else {
		p.note("not pruning: that happens in hour %s (BACKUP_PRUNE_HOUR)", hour)
	}
	if err := p.run(p.restic(env, nil, forget...)); err != nil {
		fmt.Fprintf(p.w, "\n\033[31mfailed: restic forget: %v\033[0m\n", err)
		return 1
	}
	p.say("snapshots")
	list := p.restic(env, nil, "snapshots", "--host", p.project, "--latest", "5")
	list.Stdout = p.w
	p.run(list)
	return 0
}

// hourEq is whether t's hour is the hour written, padded or not.
func hourEq(t time.Time, hour string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(hour))
	return err == nil && t.Hour() == n
}

// backupCmd is the command.
func backupCmd(args []string) int {
	root := "."
	if len(args) == 2 && args[0] == "--root" {
		root = args[1]
	} else if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "jcloud backup [--root <dir>]")
		return 2
	}
	return Backup(root, runJobCmd, os.Stdout)
}
