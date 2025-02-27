// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"path"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/cache"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/httpcache"
	"code.gitea.io/gitea/modules/lfs"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/storage"
	"code.gitea.io/gitea/routers/common"
)

// ServeBlobOrLFS download a git.Blob redirecting to LFS if necessary
func ServeBlobOrLFS(ctx *context.Context, blob *git.Blob, lastModified time.Time) error {
	if httpcache.HandleGenericETagTimeCache(ctx.Req, ctx.Resp, `"`+blob.ID.String()+`"`, lastModified) {
		return nil
	}

	dataRc, err := blob.DataAsync()
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if closed {
			return
		}
		if err = dataRc.Close(); err != nil {
			log.Error("ServeBlobOrLFS: Close: %v", err)
		}
	}()

	pointer, _ := lfs.ReadPointer(dataRc)
	if pointer.IsValid() {
		meta, _ := models.GetLFSMetaObjectByOid(ctx.Repo.Repository.ID, pointer.Oid)
		if meta == nil {
			if err = dataRc.Close(); err != nil {
				log.Error("ServeBlobOrLFS: Close: %v", err)
			}
			closed = true
			return common.ServeBlob(ctx, blob, lastModified)
		}
		if httpcache.HandleGenericETagCache(ctx.Req, ctx.Resp, `"`+pointer.Oid+`"`) {
			return nil
		}

		if setting.LFS.ServeDirect {
			// If we have a signed url (S3, object storage), redirect to this directly.
			u, err := storage.LFS.URL(pointer.RelativePath(), blob.Name())
			if u != nil && err == nil {
				ctx.Redirect(u.String())
				return nil
			}
		}

		lfsDataRc, err := lfs.ReadMetaObject(meta.Pointer)
		if err != nil {
			return err
		}
		defer func() {
			if err = lfsDataRc.Close(); err != nil {
				log.Error("ServeBlobOrLFS: Close: %v", err)
			}
		}()
		return common.ServeData(ctx, ctx.Repo.TreePath, meta.Size, lfsDataRc)
	}
	if err = dataRc.Close(); err != nil {
		log.Error("ServeBlobOrLFS: Close: %v", err)
	}
	closed = true

	return common.ServeBlob(ctx, blob, lastModified)
}

func getBlobForEntry(ctx *context.Context) (blob *git.Blob, lastModified time.Time) {
	entry, err := ctx.Repo.Commit.GetTreeEntryByPath(ctx.Repo.TreePath)
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.NotFound("GetTreeEntryByPath", err)
		} else {
			ctx.ServerError("GetTreeEntryByPath", err)
		}
		return
	}

	if entry.IsDir() || entry.IsSubModule() {
		ctx.NotFound("getBlobForEntry", nil)
		return
	}

	var c *git.LastCommitCache
	if setting.CacheService.LastCommit.Enabled && ctx.Repo.CommitsCount >= setting.CacheService.LastCommit.CommitsCount {
		c = git.NewLastCommitCache(ctx.Repo.Repository.FullName(), ctx.Repo.GitRepo, setting.LastCommitCacheTTLSeconds, cache.GetCache())
	}

	info, _, err := git.Entries([]*git.TreeEntry{entry}).GetCommitsInfo(ctx, ctx.Repo.Commit, path.Dir("/" + ctx.Repo.TreePath)[1:], c)
	if err != nil {
		ctx.ServerError("GetCommitsInfo", err)
		return
	}

	if len(info) == 1 {
		// Not Modified
		lastModified = info[0].Commit.Committer.When
	}
	blob = entry.Blob()

	return
}

// SingleDownload download a file by repos path
func SingleDownload(ctx *context.Context) {
	blob, lastModified := getBlobForEntry(ctx)
	if blob == nil {
		return
	}

	if err := common.ServeBlob(ctx, blob, lastModified); err != nil {
		ctx.ServerError("ServeBlob", err)
	}
}

// SingleDownloadOrLFS download a file by repos path redirecting to LFS if necessary
func SingleDownloadOrLFS(ctx *context.Context) {
	blob, lastModified := getBlobForEntry(ctx)
	if blob == nil {
		return
	}

	if err := ServeBlobOrLFS(ctx, blob, lastModified); err != nil {
		ctx.ServerError("ServeBlobOrLFS", err)
	}
}

// DownloadByID download a file by sha1 ID
func DownloadByID(ctx *context.Context) {
	blob, err := ctx.Repo.GitRepo.GetBlob(ctx.Params("sha"))
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.NotFound("GetBlob", nil)
		} else {
			ctx.ServerError("GetBlob", err)
		}
		return
	}
	if err = common.ServeBlob(ctx, blob, time.Time{}); err != nil {
		ctx.ServerError("ServeBlob", err)
	}
}

// DownloadByIDOrLFS download a file by sha1 ID taking account of LFS
func DownloadByIDOrLFS(ctx *context.Context) {
	blob, err := ctx.Repo.GitRepo.GetBlob(ctx.Params("sha"))
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.NotFound("GetBlob", nil)
		} else {
			ctx.ServerError("GetBlob", err)
		}
		return
	}
	if err = ServeBlobOrLFS(ctx, blob, time.Time{}); err != nil {
		ctx.ServerError("ServeBlob", err)
	}
}
