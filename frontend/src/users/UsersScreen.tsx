// SPDX-License-Identifier: Apache-2.0

import { Badge, Button, Stack, Text } from '@gophenberg/frontend-sdk'
import { __, _x, sprintf } from '@wordpress/i18n'
import { fetchUsers, setUserDisabled, usersQueryKey } from '@gopherium/react-auth/admin'
import type { User } from '@gopherium/react-auth/admin'
import { useSession } from '@gopherium/react-auth'
import { ErrorNotice, LoadingScreen, Page } from '@gopherium/godmin'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { ChangeRole } from './RoleDialog'
import { roleLabel } from './roles'

/**
 * Renders the account administration screen.
 * @returns The users screen element.
 */
export function UsersScreen() {
	const session = useSession().data
	const users = useQuery({
		queryKey: usersQueryKey,
		queryFn: ({ signal }) => fetchUsers(signal),
	})
	return (
		<Page
			title={__('Users', 'gophenberg')}
			headingLevel={2}
			actions={
				<Button variant="solid" size="compact" render={<Link to="/users/new" />}>
					{__('Add User', 'gophenberg')}
				</Button>
			}
		>
			<UsersBody
				users={users.data}
				failed={users.isError}
				currentUserId={session?.id}
				grantable={session?.grantable ?? []}
			/>
		</Page>
	)
}

/**
 * Renders the account list, or the state standing in for it.
 * @param props - The accounts, whether the read failed, the signed-in account, and the roles it may give.
 * @returns The list element, or the state element.
 */
function UsersBody({
	users,
	failed,
	currentUserId,
	grantable,
}: {
	users: User[] | undefined
	failed: boolean
	currentUserId: string | undefined
	grantable: string[]
}): ReactNode {
	if (failed) {
		return <ErrorNotice>{__('Users could not be loaded.', 'gophenberg')}</ErrorNotice>
	}
	if (users === undefined) {
		return <LoadingScreen label={__('Loading users.', 'gophenberg')} />
	}
	return (
		<div
			className="godmin-table-scroll"
			role="region"
			aria-label={__('Users', 'gophenberg')}
			tabIndex={0}
		>
			<table className="godmin-table">
				<thead>
					<tr>
						<th scope="col">{_x('Name', 'person', 'gophenberg')}</th>
						<th scope="col">{__('Email', 'gophenberg')}</th>
						<th scope="col">{_x('Role', 'account', 'gophenberg')}</th>
						<th scope="col">{_x('Status', 'account', 'gophenberg')}</th>
						<th scope="col" className="godmin-table__actions">
							{__('Actions', 'gophenberg')}
						</th>
					</tr>
				</thead>
				<tbody>
					{users.map((user) => (
						<UserRow key={user.id} user={user} isSelf={user.id === currentUserId} grantable={grantable} />
					))}
				</tbody>
			</table>
		</div>
	)
}

/**
 * Renders one account with its status, and for another account the controls changing its role and letting it in or out.
 * @param props - The account, whether it is the signed-in one, and the roles the reader may give.
 * @returns The table row element.
 */
function UserRow({ user, isSelf, grantable }: { user: User, isSelf: boolean, grantable: string[] }) {
	return (
		<tr>
			<td>{user.name}</td>
			<td>{user.email}</td>
			<td>{roleLabel(user.role)}</td>
			<td>
				<UserStatus disabled={user.disabled} />
			</td>
			<td className="godmin-table__actions">
				{isSelf ? null : (
					<div className="gophenberg-users__actions">
						<ChangeRole user={user} grantable={grantable} />
						<UserToggle user={user} />
					</div>
				)}
			</td>
		</tr>
	)
}

/**
 * Renders whether an account may log in.
 * @param props - Whether the account is shut out.
 * @returns The status badge element.
 */
function UserStatus({ disabled }: { disabled: boolean }) {
	return (
		<Badge intent={disabled ? 'none' : 'stable'}>
			{disabled ? __('Disabled', 'gophenberg') : __('Active', 'gophenberg')}
		</Badge>
	)
}

/**
 * Renders the control letting an account in or shutting it out.
 * @param props - The account the control acts on.
 * @returns The toggle element.
 */
function UserToggle({ user }: { user: User }) {
	const client = useQueryClient()
	const toggle = useMutation({
		mutationFn: () => setUserDisabled(user.id, !user.disabled),
		onSuccess: () => client.invalidateQueries({ queryKey: usersQueryKey }),
	})
	const verb = user.disabled ? __('Enable', 'gophenberg') : __('Disable', 'gophenberg')
	const named = user.disabled
		? sprintf(__('Enable %(name)s', 'gophenberg'), { name: user.name })
		: sprintf(__('Disable %(name)s', 'gophenberg'), { name: user.name })
	return (
		<Stack direction="column" gap="xs">
			<Button
				variant="outline"
				aria-label={named}
				loading={toggle.isPending}
				onClick={() => toggle.mutate()}
			>
				{verb}
			</Button>
			{toggle.isError && <Text role="alert">{__('Update failed.', 'gophenberg')}</Text>}
		</Stack>
	)
}
