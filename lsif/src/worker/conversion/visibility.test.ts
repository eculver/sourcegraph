import * as sinon from 'sinon'
import { PathVisibilityChecker } from './visibility'
import { getDirectoryChildren } from '../../shared/gitserver/gitserver'
import { range } from 'lodash'

describe('PathVisibilityChecker', () => {
    it('should test path existence in git tree', async () => {
        const children = new Map([
            ['', ['web', 'shared']],
            ['web', ['foo.ts']],
            ['web/shared', ['bonk.ts']],
            ['shared', ['bar.ts', 'baz.ts']],
        ])

        const pathVisibilityChecker = new PathVisibilityChecker({
            repositoryId: 42,
            commit: 'c',
            root: 'web',
            frontendUrl: 'frontend',
            mockGetDirectoryChildren: ({ dirname }) => Promise.resolve(new Set(children.get(dirname))),
        })

        // Test within root
        expect(await pathVisibilityChecker.shouldIncludePath('foo.ts', false)).toBeTruthy()
        expect(await pathVisibilityChecker.shouldIncludePath('bar.ts', false)).toBeFalsy()
        expect(await pathVisibilityChecker.shouldIncludePath('shared/bonk.ts', false)).toBeTruthy()

        // Test outside root but within repo
        expect(await pathVisibilityChecker.shouldIncludePath('../shared/bar.ts', false)).toBeTruthy()
        expect(await pathVisibilityChecker.shouldIncludePath('../shared/bar.ts', true)).toBeFalsy()
        expect(await pathVisibilityChecker.shouldIncludePath('../shared/bonk.ts', false)).toBeFalsy()
        expect(await pathVisibilityChecker.shouldIncludePath('../node_modules/@types/quux.ts', false)).toBeFalsy()

        // Test outside repo
        expect(await pathVisibilityChecker.shouldIncludePath('../../node_modules/@types/oops.ts', false)).toBeFalsy()
    })

    it('should cache directory contents', async () => {
        const children = new Map([['', Array.from(range(0, 100).map(i => `${i}.ts`))]])
        const mockGetDirectoryChildren = sinon.spy<typeof getDirectoryChildren>(({ dirname }) =>
            Promise.resolve(new Set(children.get(dirname)))
        )

        const pathVisibilityChecker = new PathVisibilityChecker({
            repositoryId: 42,
            commit: 'c',
            root: '',
            frontendUrl: 'frontend',
            mockGetDirectoryChildren,
        })

        for (let i = 0; i < 100; i++) {
            expect(await pathVisibilityChecker.shouldIncludePath(`${i}.ts`, false)).toBeTruthy()
            expect(await pathVisibilityChecker.shouldIncludePath(`${i}.js`, false)).toBeFalsy()
        }

        expect(mockGetDirectoryChildren.callCount).toEqual(1)
    })

    it('should early out on untracked ancestors', async () => {
        const children = new Map([['', ['not_node_modules']]])
        const mockGetDirectoryChildren = sinon.spy<typeof getDirectoryChildren>(({ dirname }) =>
            Promise.resolve(new Set(children.get(dirname)))
        )

        const pathVisibilityChecker = new PathVisibilityChecker({
            repositoryId: 42,
            commit: 'c',
            root: '',
            frontendUrl: 'frontend',
            mockGetDirectoryChildren,
        })

        for (let i = 0; i < 100; i++) {
            const path = `node_modules/${i}/deeply/nested/lib/file.ts`
            expect(await pathVisibilityChecker.shouldIncludePath(path, false)).toBeFalsy()
        }

        // Should only check children of / and /node_modules
        expect(mockGetDirectoryChildren.callCount).toEqual(2)
    })
})
