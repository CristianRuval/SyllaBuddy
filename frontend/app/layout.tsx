"use client";
import "./globals.css";
// 1. Import the Provider
import { GoogleOAuthProvider } from "@react-oauth/google";

export default function RootLayout({
                                       children,
                                   }: {
    children: React.ReactNode;
}) {
    {/*client id from google stuff here*/}
    const CLIENT_ID = "612581789040-8k3eke9705qv234rd7gej0d6h2ce1sc0.apps.googleusercontent.com"
    console.log("Client ID:", process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID);
    return (
        <html lang="en">
        <body>
        {/*wrap {children} with the Provider */}
        <GoogleOAuthProvider clientId={CLIENT_ID}>
            {children}
        </GoogleOAuthProvider>
        </body>
        </html>
    );
}
