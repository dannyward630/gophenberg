// SPDX-License-Identifier: Apache-2.0

import type { MenuItem } from '@gopherium/godmin/router'
import type { AnyRoute } from '@tanstack/react-router'

export type {
	BulkRun,
	BulkWords,
	CanvasMode,
	ConfirmBodyProps,
	FormBodyProps,
	FormKind,
	ListRun,
	ListRunState,
	RenameBodyProps,
	RunConfirmProps,
	RunSettled,
	ToasterHandle,
} from '@gopherium/godmin'
export {
	ConfirmBody,
	FormBody,
	MenuCount,
	NavigationScreen,
	RenameBody,
	RunConfirm,
	useBulkRun,
	useListRun,
	useToaster,
} from '@gopherium/godmin'
export type { MenuItem } from '@gopherium/godmin/router'
export { DrilldownItem, NavigationItem, NavigationList } from '@gopherium/godmin/router'

export interface FrontendPlugin {
	id: string
	routes: (parent: AnyRoute) => AnyRoute[]
	nav: MenuItem[]
}

export {
	AlertDialog,
	Badge,
	Button,
	Icon,
	IconButton,
	InputControl,
	Link,
	Notice,
	SelectControl,
	Skeleton,
	Stack,
	Tabs,
	Text,
	Tooltip,
	VisuallyHidden,
} from '@wordpress/ui'
export {
	audio as audioIcon,
	commentAuthorAvatar as authorIcon,
	chevronLeft as backIcon,
	backup as backupIcon,
	brush as brushIcon,
	category as categoryIcon,
	closeSmall as closeIcon,
	customPostType as customPostTypeIcon,
	chevronDown as downIcon,
	file as fileIcon,
	image as imageIcon,
	language as languageIcon,
	list as listIcon,
	listView as listViewIcon,
	media as mediaIcon,
	page as pageIcon,
	post as postIcon,
	redo as redoIcon,
	cloudUpload as saveIcon,
	settings as settingsIcon,
	drawerRight as sidebarIcon,
	drawerLeft as sidebarRtlIcon,
	trash as trashIcon,
	undo as undoIcon,
	chevronUp as upIcon,
	upload as uploadIcon,
	video as videoIcon,
} from '@wordpress/icons'
export { Dialog } from './dialog'
export { TextareaControl } from './TextareaControl'
export {
	CheckboxControl,
	DropZone,
	RadioControl,
	RangeControl,
	__experimentalStyleProvider as StyleProvider,
} from '@wordpress/components'
export { speak } from '@wordpress/a11y'
export {
	ACCESS_ADMIN,
	ADMIN,
	AUTHOR,
	CHANGE_OTHERS_WORK,
	EDITOR,
	MANAGE_SETTINGS,
	MANAGE_THEMES,
	MANAGE_TYPES,
	MANAGE_USERS,
	MEMBER,
	sessionMayChange,
} from './capabilities'
export { can, useSession } from '@gopherium/react-auth'
export type { User as Session } from '@gopherium/react-auth'
