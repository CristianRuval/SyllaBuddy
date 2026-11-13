import Link from "next/link";

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
    return (
        <div className="min-h-screen bg-white">
            {/* Top Navigation Bar */}
            <nav className="border-b bg-white px-6 py-4 flex items-center justify-between sticky top-0 z-10">
                <div className="text-xl font-bold text-blue-600">SyllaBuddy</div>
                <div className="flex gap-8 text-sm font-medium text-gray-600">
                    <Link href="/dashboard" className="hover:text-blue-600 transition">Home</Link>
                    <Link href="/dashboard/syllabi" className="hover:text-blue-600 transition">Syllabi</Link>
                    <Link href="/dashboard/calendar" className="hover:text-blue-600 transition">Calendar</Link>
                    <Link href="/dashboard/settings" className="hover:text-blue-600 transition">Settings</Link>
                </div>
                <Link href="/" className="text-sm text-gray-400 hover:text-red-500">Logout</Link>
            </nav>

            {/* Page Content */}
            <main className="p-8">{children}</main>
        </div>
    );
}